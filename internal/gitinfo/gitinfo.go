// Package gitinfo extracts the bits of git state the version engine needs.
//
// Skeleton: shells out to `git`. A future milestone may switch to go-git for
// in-process operation; see ROADMAP.md M1.
package gitinfo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// Commits enumerated in <LatestTag>..HEAD (newest first). Populated by
	// Read so the conventional-commits bump strategy can read subject lines
	// without a second gitinfo pass.
	Commits []Commit
	// InProgress names a long-running git operation if one is mid-flight:
	// "merge" / "rebase" / "cherry-pick" / "revert" / "bisect". Empty when
	// the repo is in a normal state. Honest reporting; callers refuse.
	InProgress string
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

	// Enumerate commits in <LatestTag>..HEAD so the bump strategy can read
	// them without re-shelling-out. Cost is one extra git log; negligible for
	// normal repos.
	if cs, err := CommitsSince(path, info.LatestTag); err == nil {
		info.Commits = cs
	}

	// Detect long-running git operations in progress. `git rev-parse
	// --git-dir` returns the correct path for both regular checkouts and
	// linked worktrees.
	if gd, err := run(path, "rev-parse", "--git-dir"); err == nil {
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(path, gd)
		}
		info.InProgress = detectInProgress(gd)
	}

	return info, nil
}

// detectInProgress returns the name of any git operation mid-flight, by
// looking for the well-known marker paths git creates. Empty string when the
// repo is in a normal state.
func detectInProgress(gitDir string) string {
	checks := []struct {
		path, name string
	}{
		// Order: most user-visible first.
		{"MERGE_HEAD", "merge"},
		{"rebase-merge", "rebase"},
		{"rebase-apply", "rebase"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(gitDir, c.path)); err == nil {
			return c.name
		}
	}
	return ""
}

// Commit is one entry from `git log <range>` — subject + body.
// Body is empty for single-line commits.
type Commit struct {
	Subject string
	Body    string
}

// CommitsSince returns the commits in `<tag>..HEAD`, newest first.
// If `tag` is empty, returns every commit reachable from HEAD.
// Each commit gets its subject and body separately so the conventional-commits
// parser can distinguish a `BREAKING CHANGE:` footer from a subject-line marker.
func CommitsSince(repo, tag string) ([]Commit, error) {
	revRange := "HEAD"
	if tag != "" {
		revRange = tag + "..HEAD"
	}
	// `%s` = subject, `%b` = body, `%x00` = NUL separator. Use a record
	// separator that can't appear in subjects/bodies so we can split robustly.
	const recordSep = "\x1e" // ASCII Record Separator
	const fieldSep = "\x1f"  // ASCII Unit Separator
	format := "--format=" + fieldSep + "%s" + fieldSep + "%b" + recordSep
	out, err := run(repo, "log", revRange, format)
	if err != nil {
		// git log returns non-zero when the range is empty, etc.
		// Treat as "no commits" rather than failure.
		return nil, nil
	}
	var commits []Commit
	for _, rec := range strings.Split(out, recordSep) {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, fieldSep, 3)
		if len(parts) < 3 {
			continue
		}
		commits = append(commits, Commit{
			Subject: parts[1],
			Body:    strings.TrimSpace(parts[2]),
		})
	}
	return commits, nil
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
