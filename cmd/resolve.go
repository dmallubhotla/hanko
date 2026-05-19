package cmd

import (
	"errors"
	"fmt"

	"github.com/dmallubhotla/hanko/internal/config"
	"github.com/dmallubhotla/hanko/internal/gitinfo"
	"github.com/dmallubhotla/hanko/internal/version"
)

// ErrShallow is returned when hanko refuses to operate on a shallow clone.
// `git rev-list --count` is wrong on shallow repos, so any version we compute would be wrong too — and silently wrong, which is the exact bug class hanko exists to prevent (D-004).
var ErrShallow = errors.New("shallow clone (re-clone without --depth, or use `with: { fetch-depth: 0 }` in GHA)")

// errInProgress wraps the gitinfo.Info.InProgress value into a user-facing
// error. The mid-operation state means HEAD doesn't reflect a real release
// candidate; honest refusal beats silent wrong-version.
func errInProgress(state string) error {
	return fmt.Errorf("git %s in progress; refusing to compute version. finish or abort the operation first (e.g. `git %s --abort`)", state, state)
}

// resolveVersion is the shared prelude used by every command that needs a computed version.
// It loads `.hanko.yaml` (defaults if absent), reads the repo state, refuses
// if the repo is shallow, then runs version.Compute. `bumpOverride` is a
// one-shot direction forced by `hanko version --bump`; pass "" elsewhere.
func resolveVersion(bumpOverride string) (version.Version, error) {
	cfg, err := config.Load(repoPath)
	if err != nil {
		return version.Version{}, fmt.Errorf("load config: %w", err)
	}
	info, err := gitinfo.Read(repoPath, cfg.TagMatch)
	if err != nil {
		return version.Version{}, fmt.Errorf("read git info: %w", err)
	}
	if info.Shallow {
		return version.Version{}, ErrShallow
	}
	if info.InProgress != "" {
		return version.Version{}, errInProgress(info.InProgress)
	}
	v, err := version.Compute(info, cfg, bumpOverride)
	if err != nil {
		return version.Version{}, fmt.Errorf("compute version: %w", err)
	}
	return v, nil
}

// resolveInfo is the read-only variant used by `hanko tag`, which needs the
// gitinfo for dirty / detached checks before computing. Returns the loaded
// config alongside so the caller doesn't have to re-read it.
func resolveInfo() (gitinfo.Info, *config.Config, error) {
	cfg, err := config.Load(repoPath)
	if err != nil {
		return gitinfo.Info{}, nil, fmt.Errorf("load config: %w", err)
	}
	info, err := gitinfo.Read(repoPath, cfg.TagMatch)
	if err != nil {
		return gitinfo.Info{}, nil, fmt.Errorf("read git info: %w", err)
	}
	if info.Shallow {
		return gitinfo.Info{}, nil, ErrShallow
	}
	if info.InProgress != "" {
		return gitinfo.Info{}, nil, errInProgress(info.InProgress)
	}
	return info, cfg, nil
}
