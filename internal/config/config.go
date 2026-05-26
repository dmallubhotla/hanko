// Package config loads `.hanko.yaml` from the repo root and resolves it
// against hard-coded defaults that mirror M1's behavior.
//
// Style copies kestrel's `internal/config` — single Config struct, yaml-tagged,
// explicit field-by-field merge in mergeOnDefaults. Preliminary schema; the
// shape may shift as real consumers exercise it.
//
// Hanko is project-scoped, so unlike kestrel this loader has no XDG global
// layer — the only file we read is `.hanko.yaml` somewhere at-or-above the
// requested repo path.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

const ConfigFileName = ".hanko.yaml"

// Config is the resolved, merged-with-defaults view that callers consume.
// Missing keys in `.hanko.yaml` fall back to Defaults().
type Config struct {
	// Regex applied to existing tags to extract a semver base. First capture
	// group is the semver. (D-002, future-proofed for non-`v` prefixes.)
	TagPrefix string `yaml:"tag-prefix,omitempty"`

	// Whether dirty worktree appends `.dirty` to build metadata.
	// Pointer-bool to distinguish "unset" from "explicitly false".
	DirtySuffix *bool `yaml:"dirty-suffix,omitempty"`

	// Base used when no semver tag is reachable.
	InitialVersion string `yaml:"initial-version,omitempty"`

	// "refuse" | "warn" | "ignore".
	OnShallow string `yaml:"on-shallow,omitempty"`

	// "fixed" (default) → use each branch's declared `increment`.
	// "conventional-commits" → parse <latest-tag>..HEAD subjects to choose
	// the bump direction; fall back to the branch's `increment` when no
	// commit message contributes a signal.
	BumpStrategy string `yaml:"bump-strategy,omitempty"`

	// Glob patterns passed to `git describe --match` for tag discovery.
	// Sibling to TagPrefix: the regex extracts a semver from a found tag, the
	// globs decide which tags are eligible to be found in the first place.
	// Both default to the canonical `v`-prefix-or-bare shapes.
	TagMatch []string `yaml:"tag-match,omitempty"`

	// Branch policy, evaluated in declaration order, first match wins.
	// Empty/unset → use Defaults' list.
	Branches []BranchPolicy `yaml:"branches,omitempty"`

	// Declarative stamp targets — files to mutate in lockstep at release
	// time. `hanko stamp` (no args) applies them all; `hanko seal` runs the
	// same pass before its pre-commit hooks.
	StampTargets []StampTarget `yaml:"stamp-targets,omitempty"`

	// Seal orchestration — see `hanko seal` (M5c).
	Seal SealConfig `yaml:"seal,omitempty"`

	// Source records the .hanko.yaml path that was loaded (empty if defaults).
	// Not serialised back out.
	Source string `yaml:"-"`
}

// StampTarget describes one file to update at release time.
//
// `Key` is a dotted path: top-level keys are bare names ("version"),
// nested keys (TOML) use dot separators ("project.version"). For files that
// hold multiple instances of the same release version (Chart.yaml's `version`
// and `appVersion`), use the `Keys` list instead.
//
// Engine choice per format is line-based (see docs/hanko-yaml.md and D-015).
type StampTarget struct {
	// Path is relative to the repo root.
	Path string `yaml:"path"`
	// Format selects the engine: "toml", "yaml", "json", "nix", "plain".
	Format string `yaml:"format"`
	// Key is the single dotted path to stamp. Either Key or Keys must be set.
	Key string `yaml:"key,omitempty"`
	// Keys is the list form for files where multiple paths get the same value.
	Keys []string `yaml:"keys,omitempty"`
}

// EffectiveKeys returns the keys to stamp, normalising Key/Keys into a list.
func (s StampTarget) EffectiveKeys() []string {
	if len(s.Keys) > 0 {
		return s.Keys
	}
	if s.Key != "" {
		return []string{s.Key}
	}
	return nil
}

// SealConfig configures `hanko seal`: the release-time rite that bundles
// hook execution, a single commit, an annotated tag, and a push.
type SealConfig struct {
	// PreCommit are shell commands run after the pre-flight checks pass but
	// before the release commit is created. Each runs from the repo root.
	// Failure aborts the seal, leaving the worktree as-is.
	// `{semver}`, `{full}`, `{branch}` etc. expand to fields of the computed Version.
	PreCommit []string `yaml:"pre-commit,omitempty"`

	// CommitMessage is the commit body for the release commit. Templated
	// like PreCommit. Default "chore: Release {semver}" — the `chore:` prefix
	// keeps release commits classified as no-bump under the
	// conventional-commits strategy (D-016 default), even though the commit
	// is technically behind the tag and never appears in `<tag>..HEAD`.
	CommitMessage string `yaml:"commit-message,omitempty"`

	// PushRemote is the git remote to push commit + tag to.
	// Pointer-string so an empty string in YAML (explicit disable) is
	// distinguishable from absent (use default `origin`).
	PushRemote *string `yaml:"push-remote,omitempty"`

	// RefusePrerelease, when nil or true, makes seal refuse to operate on a
	// pre-release version. Mirrors D-011 (`hanko tag` refuses prereleases).
	// Set explicitly to false for repos that want to seal pre-releases.
	RefusePrerelease *bool `yaml:"refuse-prerelease,omitempty"`
}

// BranchPolicy is one rule in the ordered list of branch matchers.
type BranchPolicy struct {
	Name       string `yaml:"name,omitempty"`
	Regex      string `yaml:"regex"`
	IsMainline bool   `yaml:"is-mainline,omitempty"`
	// "patch" | "minor" | "major" | "none".
	Increment string `yaml:"increment,omitempty"`
	// Pre-release label template; empty → no pre-release suffix (= release).
	// `{branch}` interpolates the sanitised branch name; `{N}` interpolates
	// the Nth capture group from Regex.
	Label string `yaml:"label,omitempty"`
	// 1-indexed capture-group binding for synthesised major/minor.
	// Zero = not bound (don't override the base tag's major/minor).
	MajorFrom int `yaml:"major-from,omitempty"`
	MinorFrom int `yaml:"minor-from,omitempty"`
	// Per-branch override of the global bump-strategy. Empty → inherit from
	// top-level `bump-strategy:`. Useful for "mainline reads conventional
	// commits; hotfix always bumps patch regardless of commit messages."
	BumpStrategy string `yaml:"bump-strategy,omitempty"`
}

// Defaults returns the hard-coded baseline config. M1's behaviour, expressed
// in config form, so a missing `.hanko.yaml` produces byte-identical output.
func Defaults() *Config {
	t := true
	refuseTrue := true
	defaultRemote := "origin"
	return &Config{
		TagPrefix:      `^v?(.+)$`,
		DirtySuffix:    &t,
		InitialVersion: "0.1.0",
		OnShallow:      "refuse",
		// D-016: default to reading Conventional Commits hints. Repos that
		// don't follow the convention get the parser's "no signal" verdict
		// and fall back to the per-branch `increment`, so behaviour is
		// unchanged for them — the new default only surfaces for repos that
		// already write `feat:` / `fix:` / `feat!:` style commits.
		BumpStrategy: "conventional-commits",
		Seal: SealConfig{
			CommitMessage:    "chore: Release {semver}",
			PushRemote:       &defaultRemote,
			RefusePrerelease: &refuseTrue,
		},
		TagMatch: []string{
			`v[0-9]*.[0-9]*.[0-9]*`,
			`[0-9]*.[0-9]*.[0-9]*`,
		},
		Branches: []BranchPolicy{
			{Name: "mainline", Regex: `^(main|master)$`, IsMainline: true, Increment: "patch", Label: ""},
			{Name: "release", Regex: `^release/(\d+)\.(\d+)$`, IsMainline: true, Increment: "patch", Label: "", MajorFrom: 1, MinorFrom: 2},
			{Name: "hotfix", Regex: `^hotfix/.*$`, Increment: "patch", Label: "hotfix"},
			{Name: "feature", Regex: `.*`, Increment: "none", Label: "{branch}"},
		},
	}
}

// Load walks up from `startDir` looking for `.hanko.yaml`. If absent, returns
// Defaults() with Source="". If present, parses it and merges user-provided
// fields onto Defaults().
func Load(startDir string) (*Config, error) {
	path := findConfigFile(startDir)
	if path == "" {
		return Defaults(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var user Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&user); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validate(&user); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := mergeOnDefaults(&user)
	out.Source = path
	return out, nil
}

// validate checks user-supplied fields for value-level errors that the YAML
// parser can't catch on its own (enum values, regex syntax, mutually-exclusive
// keys). It runs against the user's raw input — defaults are hard-coded and
// trusted — so error messages point at keys the user actually wrote.
func validate(c *Config) error {
	if c.OnShallow != "" {
		switch c.OnShallow {
		case "refuse", "warn", "ignore":
		default:
			return fmt.Errorf("on-shallow: %q is not one of refuse|warn|ignore", c.OnShallow)
		}
	}
	if c.BumpStrategy != "" {
		if err := validateBumpStrategy(c.BumpStrategy); err != nil {
			return fmt.Errorf("bump-strategy: %w", err)
		}
	}
	if c.TagPrefix != "" {
		if _, err := regexp.Compile(c.TagPrefix); err != nil {
			return fmt.Errorf("tag-prefix: %q: %w", c.TagPrefix, err)
		}
	}
	for i, b := range c.Branches {
		if b.Regex == "" {
			return fmt.Errorf("branches[%d].regex: required", i)
		}
		if _, err := regexp.Compile(b.Regex); err != nil {
			return fmt.Errorf("branches[%d].regex: %q: %w", i, b.Regex, err)
		}
		if b.Increment != "" {
			switch b.Increment {
			case "patch", "minor", "major", "none":
			default:
				return fmt.Errorf("branches[%d].increment: %q is not one of patch|minor|major|none", i, b.Increment)
			}
		}
		if b.BumpStrategy != "" {
			if err := validateBumpStrategy(b.BumpStrategy); err != nil {
				return fmt.Errorf("branches[%d].bump-strategy: %w", i, err)
			}
		}
	}
	for i, s := range c.StampTargets {
		if s.Path == "" {
			return fmt.Errorf("stamp-targets[%d].path: required", i)
		}
		switch s.Format {
		case "":
			return fmt.Errorf("stamp-targets[%d].format: required (one of toml|yaml|json|nix|plain)", i)
		case "toml", "yaml", "json", "nix", "plain":
		default:
			return fmt.Errorf("stamp-targets[%d].format: %q is not one of toml|yaml|json|nix|plain", i, s.Format)
		}
		if s.Key != "" && len(s.Keys) > 0 {
			return fmt.Errorf("stamp-targets[%d]: set either key: or keys:, not both", i)
		}
		if s.Key == "" && len(s.Keys) == 0 {
			return fmt.Errorf("stamp-targets[%d]: must set key: or keys:, neither was set", i)
		}
	}
	return nil
}

func validateBumpStrategy(s string) error {
	switch s {
	case "fixed", "conventional-commits":
		return nil
	default:
		return fmt.Errorf("%q is not one of fixed|conventional-commits", s)
	}
}

// findConfigFile walks from `start` up to filesystem root looking for
// .hanko.yaml. Returns the first match's absolute path, or "" if none.
func findConfigFile(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, ConfigFileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// mergeOnDefaults overlays user-provided fields onto Defaults(). Scalar
// fields use "non-zero overrides"; the Branches list is replace-not-merge
// (declaring a custom list fully replaces the default rules, per the
// design-principles "first match wins" rule).
func mergeOnDefaults(user *Config) *Config {
	out := Defaults()
	if user.TagPrefix != "" {
		out.TagPrefix = user.TagPrefix
	}
	if user.DirtySuffix != nil {
		out.DirtySuffix = user.DirtySuffix
	}
	if user.InitialVersion != "" {
		out.InitialVersion = user.InitialVersion
	}
	if user.OnShallow != "" {
		out.OnShallow = user.OnShallow
	}
	if user.BumpStrategy != "" {
		out.BumpStrategy = user.BumpStrategy
	}
	if len(user.TagMatch) > 0 {
		out.TagMatch = user.TagMatch
	}
	if len(user.Branches) > 0 {
		out.Branches = user.Branches
	}
	if len(user.StampTargets) > 0 {
		out.StampTargets = user.StampTargets
	}
	if len(user.Seal.PreCommit) > 0 {
		out.Seal.PreCommit = user.Seal.PreCommit
	}
	if user.Seal.CommitMessage != "" {
		out.Seal.CommitMessage = user.Seal.CommitMessage
	}
	if user.Seal.PushRemote != nil {
		out.Seal.PushRemote = user.Seal.PushRemote
	}
	if user.Seal.RefusePrerelease != nil {
		out.Seal.RefusePrerelease = user.Seal.RefusePrerelease
	}
	return out
}
