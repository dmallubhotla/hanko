// Package gittag wraps the write-side git tag operations hanko needs.
//
// gitinfo is the read-only sibling; this package contains the mutations.
// Shells out to `git`, same as gitinfo, for now.
package gittag

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrTagConflict means a tag with the requested name exists but does not
// point at HEAD. Callers should not silently retag — that's a deliberate
// human decision.
var ErrTagConflict = errors.New("tag exists pointing at a different commit")

// AtHead returns true when `name` is one of the tags pointing at HEAD.
func AtHead(repo, name string) (bool, error) {
	out, err := run(repo, "tag", "--points-at", "HEAD")
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// Exists returns true when a tag with `name` exists anywhere in the repo.
func Exists(repo, name string) (bool, error) {
	// `show-ref --tags <name>` exits non-zero if the tag doesn't exist;
	// that's not an error condition for us.
	cmd := exec.Command("git", "show-ref", "--tags", "--verify", "--quiet", "refs/tags/"+name)
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Create makes an annotated tag at HEAD. If sign is true, the tag is GPG
// signed (`-s` instead of `-a`); git's own gpg config decides the key.
//
// Caller must already have established that no tag of this name exists at a
// different commit — Create does not check ErrTagConflict itself.
func Create(repo, name, message string, sign bool) error {
	flag := "-a"
	if sign {
		flag = "-s"
	}
	_, err := run(repo, "tag", flag, name, "-m", message)
	return err
}

// Push sends a single tag to the named remote.
func Push(repo, remote, name string) error {
	_, err := run(repo, "push", remote, "refs/tags/"+name)
	return err
}

// EnsureAtHead is the idempotency-friendly compound operation:
//   - if `name` already points at HEAD, no-op (returns alreadyExists=true, conflict=false)
//   - if `name` exists pointing elsewhere, returns ErrTagConflict
//   - otherwise creates the annotated tag and returns alreadyExists=false
//
// Returning the alreadyExists flag lets callers print a "tag already exists"
// notice instead of a "created" notice without re-querying.
func EnsureAtHead(repo, name, message string, sign bool) (alreadyExists bool, err error) {
	atHead, err := AtHead(repo, name)
	if err != nil {
		return false, fmt.Errorf("check tag at head: %w", err)
	}
	if atHead {
		return true, nil
	}
	exists, err := Exists(repo, name)
	if err != nil {
		return false, fmt.Errorf("check tag exists: %w", err)
	}
	if exists {
		return false, ErrTagConflict
	}
	if err := Create(repo, name, message, sign); err != nil {
		return false, fmt.Errorf("create tag: %w", err)
	}
	return false, nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
