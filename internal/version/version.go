// Package version computes a semantic version from git history.
//
// The branch policy applied here is the M1 default; configurability via
// .hanko.yaml is a later milestone. See ROADMAP.md.
package version

import (
	"fmt"
	"regexp"
	"strings"

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

// Compute derives a version from the given gitinfo snapshot.
//
// Branch policy (M1 defaults, see ROADMAP.md):
//
//   - main / master → <major>.<minor>.<patch+commits>
//   - release/x.y   → <x>.<y>.<patch+commits>
//   - hotfix/*      → <major>.<minor>.<patch+1>-hotfix.<commits>
//   - everything    → <base>-<sanitized-branch>.<commits>  (pre-release)
//
// When no tag is reachable the base is 0.1.0 and a pre-release suffix is always emitted — the absence of a tag is itself a signal worth surfacing.
//
// D-001: a detached HEAD pointing at a tagged commit emits the tag's version verbatim.
// This is the canonical GHA tag-push case ("build the release at this tag"); the branch context is genuinely ambiguous from git alone but the intent is clear.
func Compute(info gitinfo.Info) (Version, error) {
	if info.Detached && info.LatestTag != "" && info.CommitsSinceTag == 0 {
		return versionFromTagAtHead(info), nil
	}

	base, _ := parseSemverTag(info.LatestTag)
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

	switch {
	case info.LatestTag == "":
		// No tag in repo at all. 0.1.0 base, always a pre-release until a
		// human creates the first tag.
		v.Major, v.Minor, v.Patch = 0, 1, 0
		v.PreRelease = fmt.Sprintf("%s.%d", sanitizeBranch(branch), n)

	case isMainline(branch):
		v.Major = base.major
		v.Minor = base.minor
		v.Patch = base.patch + n

	case isReleaseBranch(branch):
		x, y, ok := parseReleaseBranch(branch)
		if !ok {
			// Malformed `release/...` — fall through to feature-branch
			// handling. Decision D-003 territory.
			v.Major = base.major
			v.Minor = base.minor
			v.Patch = base.patch
			v.PreRelease = fmt.Sprintf("%s.%d", sanitizeBranch(branch), n)
			break
		}
		v.Major = x
		v.Minor = y
		v.Patch = base.patch + n

	case isHotfixBranch(branch):
		v.Major = base.major
		v.Minor = base.minor
		v.Patch = base.patch + 1
		v.PreRelease = fmt.Sprintf("hotfix.%d", n)

	default:
		v.Major = base.major
		v.Minor = base.minor
		v.Patch = base.patch
		v.PreRelease = fmt.Sprintf("%s.%d", sanitizeBranch(branch), n)
	}

	v.IsPreRelease = v.PreRelease != ""
	v.BuildMetadata = fmt.Sprintf("%d.%s", n, info.ShortSha)
	if info.Dirty {
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
func versionFromTagAtHead(info gitinfo.Info) Version {
	base, _ := parseSemverTag(info.LatestTag)

	// Extract the substring of the tag after `v?<M.m.p>`.
	tag := strings.TrimPrefix(info.LatestTag, "v")
	corePrefix := fmt.Sprintf("%d.%d.%d", base.major, base.minor, base.patch)
	suffix := strings.TrimPrefix(tag, corePrefix)

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

type semverBase struct{ major, minor, patch int }

// tagRE matches `v?MAJOR.MINOR.PATCH` and ignores any pre-release / build
// metadata suffix on the tag itself. M1 only consumes the numeric base.
var tagRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func parseSemverTag(tag string) (semverBase, bool) {
	if tag == "" {
		return semverBase{0, 0, 0}, false
	}
	m := tagRE.FindStringSubmatch(tag)
	if m == nil {
		return semverBase{0, 0, 0}, false
	}
	var b semverBase
	fmt.Sscanf(m[1], "%d", &b.major)
	fmt.Sscanf(m[2], "%d", &b.minor)
	fmt.Sscanf(m[3], "%d", &b.patch)
	return b, true
}

func isMainline(branch string) bool {
	return branch == "main" || branch == "master"
}

var releaseRE = regexp.MustCompile(`^release/(\d+)\.(\d+)$`)

func isReleaseBranch(b string) bool { return releaseRE.MatchString(b) }

func parseReleaseBranch(b string) (int, int, bool) {
	m := releaseRE.FindStringSubmatch(b)
	if m == nil {
		return 0, 0, false
	}
	var x, y int
	fmt.Sscanf(m[1], "%d", &x)
	fmt.Sscanf(m[2], "%d", &y)
	return x, y, true
}

func isHotfixBranch(b string) bool { return strings.HasPrefix(b, "hotfix/") }

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
