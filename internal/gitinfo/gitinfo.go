// Package gitinfo extracts the bits of git state the version engine needs.
//
// Skeleton: shells out to `git`. A future milestone may switch to go-git for
// in-process operation; see ROADMAP.md M1.
package gitinfo

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Info is a snapshot of the relevant repo state at invocation time.
type Info struct {
	Branch          string
	Sha             string
	ShortSha        string
	LatestTag       string
	CommitsSinceTag int
	Dirty           bool
	Shallow         bool
	Detached        bool
	// CommitDate is HEAD's committer date in RFC 3339 form (e.g.
	// "2026-01-01T00:00:00Z"). Used as `org.opencontainers.image.created`
	// and for `-X main.date=...` ldflags. Deterministic per HEAD.
	CommitDate string
}

// ErrNoCommits indicates the repo has been initialised but has no commits.
// There is no meaningful version to compute in that state.
var ErrNoCommits = errors.New("repository has no commits")

// Read collects the git info for the repo rooted at path.
//
// `tagMatchGlobs` filters which tags are eligible to be returned by
// `git describe --match`. Pass `config.Defaults().TagMatch` for the
// canonical `v`-prefix-or-bare set, or `nil` to skip filtering entirely.
//
// Errors are returned only when git itself is broken or the repo has no
// commits; "absent but expected" states (no tags, detached HEAD) are
// surfaced as empty fields or booleans, not errors.
func Read(path string, tagMatchGlobs []string) (Info, error) {
	info := Info{}

	// HEAD must resolve, or there's nothing to do. Distinguish "no commits"
	// from "git is broken" so callers can decide how to react.
	sha, err := run(path, "rev-parse", "HEAD")
	if err != nil {
		if _, e := run(path, "rev-parse", "--is-inside-work-tree"); e == nil {
			return info, ErrNoCommits
		}
		return info, fmt.Errorf("rev-parse HEAD: %w", err)
	}
	info.Sha = sha

	if out, err := run(path, "rev-parse", "--short", "HEAD"); err == nil {
		info.ShortSha = out
	}

	// Branch resolution: --abbrev-ref returns "HEAD" when detached. Treat
	// that as Detached + empty Branch; let the version engine fall back.
	if out, err := run(path, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if out == "HEAD" {
			info.Detached = true
		} else {
			info.Branch = out
		}
	}

	// Latest reachable tag matching a semver shape.
	// `tagMatchGlobs` are passed through to `describe --match` so we skip
	// marker tags (e.g. `release-frozen`) at the source.
	// `describe` errors if no matching tag is reachable; that's a normal
	// state, not a failure (D-012).
	describeArgs := []string{"describe", "--tags", "--abbrev=0"}
	for _, g := range tagMatchGlobs {
		describeArgs = append(describeArgs, "--match", g)
	}
	if out, err := run(path, describeArgs...); err == nil {
		info.LatestTag = out
	}

	// Commit count since the latest tag, or total commits from root if no tag.
	var revRange string
	if info.LatestTag != "" {
		revRange = info.LatestTag + "..HEAD"
	} else {
		revRange = "HEAD"
	}
	if out, err := run(path, "rev-list", "--count", revRange); err == nil {
		if n, perr := strconv.Atoi(out); perr == nil {
			info.CommitsSinceTag = n
		}
	}

	if out, err := run(path, "status", "--porcelain"); err == nil {
		info.Dirty = out != ""
	}

	if out, err := run(path, "rev-parse", "--is-shallow-repository"); err == nil {
		info.Shallow = out == "true"
	}

	if out, err := run(path, "log", "-1", "--format=%cI", "HEAD"); err == nil {
		info.CommitDate = out
	}

	return info, nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
