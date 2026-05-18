package cmd

import (
	"errors"
	"fmt"

	"github.com/dmallubhotla/hanko/internal/gitinfo"
	"github.com/dmallubhotla/hanko/internal/version"
)

// ErrShallow is returned when hanko refuses to operate on a shallow clone.
// `git rev-list --count` is wrong on shallow repos, so any version we compute would be wrong too — and silently wrong, which is the exact bug class hanko exists to prevent (D-004).
var ErrShallow = errors.New("shallow clone (re-clone without --depth, or use `with: { fetch-depth: 0 }` in GHA)")

// resolveVersion is the shared prelude used by every command that needs a computed version.
// It reads the repo state, refuses if the repo is shallow, then runs version.Compute.
func resolveVersion() (version.Version, error) {
	info, err := gitinfo.Read(repoPath)
	if err != nil {
		return version.Version{}, fmt.Errorf("read git info: %w", err)
	}
	if info.Shallow {
		return version.Version{}, ErrShallow
	}
	v, err := version.Compute(info)
	if err != nil {
		return version.Version{}, fmt.Errorf("compute version: %w", err)
	}
	return v, nil
}

// resolveInfo is the read-only variant used by `hanko tag`, which needs the gitinfo for dirty / detached checks before computing.
func resolveInfo() (gitinfo.Info, error) {
	info, err := gitinfo.Read(repoPath)
	if err != nil {
		return gitinfo.Info{}, fmt.Errorf("read git info: %w", err)
	}
	if info.Shallow {
		return gitinfo.Info{}, ErrShallow
	}
	return info, nil
}
