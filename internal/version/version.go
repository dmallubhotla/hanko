// Package version computes a semantic version from git history.
//
// The branch-policy evaluator reads from a config.Config — callers pass
// config.Defaults() when no `.hanko.yaml` is loaded. The defaults reproduce
// M1's hard-coded behaviour byte-for-byte.
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dmallubhotla/hanko/internal/config"
	"github.com/dmallubhotla/hanko/internal/gitinfo"
)

// Version is the canonical version output. Field set is loosely modelled on
// GitVersion so existing consumers can be ported with minimal changes.
type Version struct {
	Major         int    `json:"major"`
	Minor         int    `json:"minor"`
	Patch         int    `json:"patch"`
	PreRelease    string `json:"preRelease,omitempty"`
	BuildMetadata string `json:"buildMetadata,omitempty"`

	SemVer     string `json:"semVer"`     // e.g. 1.2.3-alpha.1
	FullSemVer string `json:"fullSemVer"` // e.g. 1.2.3-alpha.1+5.abc1234

	BranchName string `json:"branchName"`
	Sha        string `json:"sha"`
	ShortSha   string `json:"shortSha"`
	CommitDate string `json:"commitDate,omitempty"`

	IsPreRelease bool `json:"isPreRelease"`
}

// Compute derives a version from the given gitinfo snapshot, evaluated
// against the supplied config. Pass config.Defaults() when no `.hanko.yaml`
// is loaded — that path reproduces M1's behaviour exactly.
//
// D-001: a detached HEAD pointing at a tagged commit emits the tag's version
// verbatim. This special case is invoked-policy, not branch-policy, so it
// short-circuits before the config-driven branch evaluator.
func Compute(info gitinfo.Info, cfg *config.Config) (Version, error) {
	if info.Detached && info.LatestTag != "" && info.CommitsSinceTag == 0 {
		return versionFromTagAtHead(info, cfg.TagPrefix), nil
	}

	base, _ := parseSemverTag(info.LatestTag, cfg.TagPrefix)
	branch := info.Branch
	if branch == "" {
		branch = "detached"
	}

	v := Version{
		BranchName: branch,
		Sha:        info.Sha,
		ShortSha:   info.ShortSha,
		CommitDate: info.CommitDate,
	}

	n := info.CommitsSinceTag

	if info.LatestTag == "" {
		// No tag in repo at all. Use initial-version as base; always a
		// pre-release until a human creates the first tag (or runs
		// `hanko tag --initial`).
		initial, _ := parseSemverTag(cfg.InitialVersion, cfg.TagPrefix)
		v.Major, v.Minor, v.Patch = initial.major, initial.minor, initial.patch
		v.PreRelease = fmt.Sprintf("%s.%d", sanitizeBranch(branch), n)
	} else {
		policy, captures := matchBranch(cfg.Branches, branch)
		applyPolicy(&v, policy, captures, base, branch, n)
	}

	v.IsPreRelease = v.PreRelease != ""
	v.BuildMetadata = fmt.Sprintf("%d.%s", n, info.ShortSha)
	if info.Dirty && dirtySuffixEnabled(cfg) {
		v.BuildMetadata += ".dirty"
	}

	v.SemVer = composeSemVer(v.Major, v.Minor, v.Patch, v.PreRelease)
	v.FullSemVer = v.SemVer
	if v.BuildMetadata != "" {
		v.FullSemVer += "+" + v.BuildMetadata
	}

	return v, nil
}

// AsEnv flattens the version into HANKO_* environment variables, suitable for
// `eval $(hanko version --format env)` in shell scripts.
func (v Version) AsEnv() map[string]string {
	return map[string]string{
		"HANKO_SEMVER":        v.SemVer,
		"HANKO_FULL_SEMVER":   v.FullSemVer,
		"HANKO_MAJOR":         fmt.Sprintf("%d", v.Major),
		"HANKO_MINOR":         fmt.Sprintf("%d", v.Minor),
		"HANKO_PATCH":         fmt.Sprintf("%d", v.Patch),
		"HANKO_BRANCH":        v.BranchName,
		"HANKO_SHA":           v.Sha,
		"HANKO_SHORT_SHA":     v.ShortSha,
		"HANKO_IS_PRERELEASE": fmt.Sprintf("%t", v.IsPreRelease),
	}
}

// AsGHA returns the lowercase-dashed field set that cicd's resolve-version
// composite action contract uses, ready to be appended to $GITHUB_OUTPUT.
// Field names are deliberately frozen — see docs/design-decisions.md D-006.
func (v Version) AsGHA() map[string]string {
	return map[string]string{
		"full":          v.SemVer,
		"major":         fmt.Sprintf("%d", v.Major),
		"minor":         fmt.Sprintf("%d", v.Minor),
		"patch":         fmt.Sprintf("%d", v.Patch),
		"major-minor":   fmt.Sprintf("%d.%d", v.Major, v.Minor),
		"short-sha":     v.ShortSha,
		"is-prerelease": fmt.Sprintf("%t", v.IsPreRelease),
		"branch":        sanitizeBranch(v.BranchName),
	}
}

// --- internals -------------------------------------------------------------

// versionFromTagAtHead returns the Version for the D-001 special case: HEAD is detached AND there is a tag pointing at HEAD.
// We emit the tag's version exactly (including prerelease / build-metadata suffix), rather than running the branch-policy fallback that would mark it as a "detached" pre-release.
func versionFromTagAtHead(info gitinfo.Info, prefixRegex string) Version {
	base, _ := parseSemverTag(info.LatestTag, prefixRegex)

	// Strip the prefix the same way parseSemverTag did, then peel off the
	// numeric core to leave just the pre-release / build-metadata suffix.
	stripped := stripTagPrefix(info.LatestTag, prefixRegex)
	corePrefix := fmt.Sprintf("%d.%d.%d", base.major, base.minor, base.patch)
	suffix := strings.TrimPrefix(stripped, corePrefix)

	pre, buildMeta := "", ""
	if rest, ok := strings.CutPrefix(suffix, "-"); ok {
		if before, after, found := strings.Cut(rest, "+"); found {
			pre, buildMeta = before, after
		} else {
			pre = rest
		}
	} else if rest, ok := strings.CutPrefix(suffix, "+"); ok {
		buildMeta = rest
	}

	if info.Dirty {
		if buildMeta == "" {
			buildMeta = "dirty"
		} else {
			buildMeta += ".dirty"
		}
	}

	v := Version{
		Major:         base.major,
		Minor:         base.minor,
		Patch:         base.patch,
		PreRelease:    pre,
		BuildMetadata: buildMeta,
		BranchName:    "detached",
		Sha:           info.Sha,
		ShortSha:      info.ShortSha,
		CommitDate:    info.CommitDate,
		IsPreRelease:  pre != "",
	}
	v.SemVer = composeSemVer(v.Major, v.Minor, v.Patch, v.PreRelease)
	v.FullSemVer = v.SemVer
	if v.BuildMetadata != "" {
		v.FullSemVer += "+" + v.BuildMetadata
	}
	return v
}

// matchBranch walks the configured branch policies in order, returns the
// first whose regex matches the branch (plus that regex's capture groups,
// captures[0] = full match). Returns a permissive fallback if nothing matched.
func matchBranch(policies []config.BranchPolicy, branch string) (config.BranchPolicy, []string) {
	for _, p := range policies {
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			continue
		}
		m := re.FindStringSubmatch(branch)
		if m != nil {
			return p, m
		}
	}
	return config.BranchPolicy{Increment: "none", Label: "{branch}"}, []string{branch}
}

// applyPolicy fills v.{Major,Minor,Patch,PreRelease} based on the matched
// policy. Two shapes: mainline (commits advance the core), non-mainline
// (static bump + pre-release counter from commit count).
func applyPolicy(v *Version, p config.BranchPolicy, captures []string, base semverBase, branch string, n int) {
	major, minor, patch := base.major, base.minor, base.patch

	// Capture-group bindings (1-indexed; captures[0] is full match).
	if p.MajorFrom > 0 && p.MajorFrom < len(captures) {
		if x, err := strconv.Atoi(captures[p.MajorFrom]); err == nil {
			major = x
		}
	}
	if p.MinorFrom > 0 && p.MinorFrom < len(captures) {
		if y, err := strconv.Atoi(captures[p.MinorFrom]); err == nil {
			minor = y
		}
	}

	if p.IsMainline {
		// Continuous-delivery: commits advance the core; no pre-release counter unless Label is set.
		switch p.Increment {
		case "minor":
			minor += n
			patch = 0
		case "major":
			major += n
			minor = 0
			patch = 0
		default: // "patch" and ""
			patch += n
		}
	} else {
		// Static bump (one-time), pre-release counter carries the commit count.
		switch p.Increment {
		case "patch":
			patch++
		case "minor":
			minor++
			patch = 0
		case "major":
			major++
			minor = 0
			patch = 0
			// "none" / "" → no bump
		}
	}

	v.Major, v.Minor, v.Patch = major, minor, patch
	if p.Label != "" {
		v.PreRelease = fmt.Sprintf("%s.%d", expandLabel(p.Label, branch, captures), n)
	}
}

// expandLabel substitutes `{branch}` and `{N}` (capture-group references)
// in a label template.
var captureGroupRE = regexp.MustCompile(`\{(\d+)\}`)

func expandLabel(template, branch string, captures []string) string {
	s := strings.ReplaceAll(template, "{branch}", sanitizeBranch(branch))
	s = captureGroupRE.ReplaceAllStringFunc(s, func(m string) string {
		idx, _ := strconv.Atoi(m[1 : len(m)-1])
		if idx >= 0 && idx < len(captures) {
			return captures[idx]
		}
		return m
	})
	return s
}

func dirtySuffixEnabled(cfg *config.Config) bool {
	return cfg.DirtySuffix == nil || *cfg.DirtySuffix
}

type semverBase struct{ major, minor, patch int }

// semverCoreRE pulls `MAJOR.MINOR.PATCH` out of a string that's already had
// the prefix regex applied. Pre-release / build metadata on the tag itself
// are deliberately ignored at this layer.
var semverCoreRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// parseSemverTag strips the configured prefix from `tag`, then extracts
// MAJOR.MINOR.PATCH from what remains. Returns the zero base when either
// step fails — callers downgrade to no-tag treatment in that case.
func parseSemverTag(tag, prefixRegex string) (semverBase, bool) {
	if tag == "" {
		return semverBase{0, 0, 0}, false
	}
	stripped := stripTagPrefix(tag, prefixRegex)
	m := semverCoreRE.FindStringSubmatch(stripped)
	if m == nil {
		return semverBase{0, 0, 0}, false
	}
	var b semverBase
	fmt.Sscanf(m[1], "%d", &b.major)
	fmt.Sscanf(m[2], "%d", &b.minor)
	fmt.Sscanf(m[3], "%d", &b.patch)
	return b, true
}

// stripTagPrefix returns the portion of `tag` after the configured
// prefix-regex's first capture group consumes its prefix. If the regex
// doesn't match or is empty/broken, the input is returned unchanged.
func stripTagPrefix(tag, prefixRegex string) string {
	if prefixRegex == "" {
		return tag
	}
	re, err := regexp.Compile(prefixRegex)
	if err != nil {
		return tag
	}
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return tag
	}
	if len(m) >= 2 {
		return m[1]
	}
	return m[0]
}

// sanitizeBranch lowercases and replaces runs of non-alphanumerics with a
// single `-`. Matches GitVersion's behaviour closely enough for tag-portable
// pre-release labels.
var nonAlnumRE = regexp.MustCompile(`[^a-z0-9]+`)

func sanitizeBranch(b string) string {
	s := strings.ToLower(b)
	s = nonAlnumRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "branch"
	}
	return s
}

func composeSemVer(maj, min, pat int, pre string) string {
	core := fmt.Sprintf("%d.%d.%d", maj, min, pat)
	if pre == "" {
		return core
	}
	return core + "-" + pre
}
