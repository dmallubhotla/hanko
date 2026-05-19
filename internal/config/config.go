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
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const ConfigFileName = ".hanko.yaml"

// Config is the resolved, merged-with-defaults view that callers consume.
// Missing keys in `.hanko.yaml` fall back to Defaults().
type Config struct {
	// Regex applied to existing tags to extract a semver base. First capture
	// group is the semver. (D-002, future-proofed for non-`v` prefixes.)
	TagPrefix string `yaml:"tag-prefix,omitempty"`

	// "continuous-delivery" (current M1 behaviour) or "mainline" (every commit
	// on a mainline branch bumps patch; gitversion-compat).
	Mode string `yaml:"mode,omitempty"`

	// Whether dirty worktree appends `.dirty` to build metadata.
	// Pointer-bool to distinguish "unset" from "explicitly false".
	DirtySuffix *bool `yaml:"dirty-suffix,omitempty"`

	// Base used when no semver tag is reachable.
	InitialVersion string `yaml:"initial-version,omitempty"`

	// "refuse" | "warn" | "ignore".
	OnShallow string `yaml:"on-shallow,omitempty"`

	// Glob patterns passed to `git describe --match` for tag discovery.
	// Sibling to TagPrefix: the regex extracts a semver from a found tag, the
	// globs decide which tags are eligible to be found in the first place.
	// Both default to the canonical `v`-prefix-or-bare shapes.
	TagMatch []string `yaml:"tag-match,omitempty"`

	// Branch policy, evaluated in declaration order, first match wins.
	// Empty/unset → use Defaults' list.
	Branches []BranchPolicy `yaml:"branches,omitempty"`

	// Source records the .hanko.yaml path that was loaded (empty if defaults).
	// Not serialised back out.
	Source string `yaml:"-"`
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
}

// Defaults returns the hard-coded baseline config. M1's behaviour, expressed
// in config form, so a missing `.hanko.yaml` produces byte-identical output.
func Defaults() *Config {
	t := true
	return &Config{
		TagPrefix:      `^v?(.+)$`,
		Mode:           "continuous-delivery",
		DirtySuffix:    &t,
		InitialVersion: "0.1.0",
		OnShallow:      "refuse",
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
	if err := yaml.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := mergeOnDefaults(&user)
	out.Source = path
	return out, nil
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
	if user.Mode != "" {
		out.Mode = user.Mode
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
	if len(user.TagMatch) > 0 {
		out.TagMatch = user.TagMatch
	}
	if len(user.Branches) > 0 {
		out.Branches = user.Branches
	}
	return out
}
