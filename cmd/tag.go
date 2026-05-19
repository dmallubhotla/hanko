package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/dmallubhotla/hanko/internal/gittag"
	"github.com/dmallubhotla/hanko/internal/version"
	"github.com/spf13/cobra"
)

var (
	tagPush    bool
	tagRemote  string
	tagDryRun  bool
	tagForce   bool
	tagMessage string
	tagSign    bool
	tagInitial string
)

// initialRE accepts the same shapes the version engine recognises:
// optional `v` prefix, semver core, optional pre-release / build metadata.
var initialRE = regexp.MustCompile(`^v?\d+\.\d+\.\d+([-+][0-9A-Za-z.\-+]+)?$`)

var tagCmd = &cobra.Command{
	Use:     "tag",
	Short:   "Create a git tag for the computed version",
	GroupID: "stamp",
	RunE: func(cmd *cobra.Command, args []string) error {
		info, cfg, err := resolveInfo()
		if err != nil {
			return err
		}

		var name string
		if tagInitial != "" {
			// D-011 (revised): `--initial` is the one narrow exception to the
			// "never tag a pre-release" rule. Only valid in the bootstrap
			// state (no prior semver-shaped tag), takes the value verbatim so
			// the caller decides the v-prefix policy for their repo.
			atHead, err := gittag.AtHead(repoPath, tagInitial)
			if err != nil {
				return fmt.Errorf("check tag at head: %w", err)
			}
			if atHead {
				fmt.Println(tagInitial)
				return nil
			}
			if info.LatestTag != "" {
				return fmt.Errorf("--initial only valid when no semver-shaped tag exists (found %q)", info.LatestTag)
			}
			if !initialRE.MatchString(tagInitial) {
				return fmt.Errorf("--initial %q is not a semver-shaped tag (want v?MAJOR.MINOR.PATCH[-pre][+meta])", tagInitial)
			}
			name = tagInitial
		} else {
			v, err := version.Compute(info, cfg)
			if err != nil {
				return fmt.Errorf("compute version: %w", err)
			}
			// D-002: follow the existing repo's tag-prefix convention.
			// If the latest reachable tag is bare (`1.2.3`), keep new tags bare; if `v`-prefixed, keep them `v`-prefixed.
			// The bootstrap case is handled by `--initial` (verbatim value).
			prefix := ""
			if strings.HasPrefix(info.LatestTag, "v") {
				prefix = "v"
			}
			name = prefix + v.SemVer

			// Pre-flight checks.
			// Order matters: cheap state checks before any git mutation, and the idempotency check before the conflict check (because "already at HEAD" looks like a conflict to naive tag-existence queries).
			atHead, err := gittag.AtHead(repoPath, name)
			if err != nil {
				return fmt.Errorf("check tag at head: %w", err)
			}
			if atHead {
				fmt.Println(name)
				return nil
			}

			// D-011: hanko tag never creates pre-release tags via computation.
			// The bootstrap case (first release in a fresh repo) is handled by `--initial <version>`.
			if v.IsPreRelease {
				return fmt.Errorf("computed version %q is a pre-release; `hanko tag` only tags releases (merge to mainline first, or use `--initial <version>` for the first release)", v.SemVer)
			}
		}

		if info.Dirty && !tagForce {
			return fmt.Errorf("worktree is dirty; refusing to tag (pass --force to override)")
		}

		exists, err := gittag.Exists(repoPath, name)
		if err != nil {
			return fmt.Errorf("check tag exists: %w", err)
		}
		if exists {
			if tagInitial != "" {
				return fmt.Errorf("tag %q already exists", name)
			}
			// Computed path: AtHead was checked and returned false, so the
			// existing tag is at a different commit.
			return fmt.Errorf("tag %q exists but does not point at HEAD", name)
		}

		message := tagMessage
		if message == "" {
			message = "Release " + name
		}

		if tagDryRun {
			fmt.Printf("would create annotated tag %s on %s\n", name, info.Sha)
			if tagPush {
				fmt.Printf("would push %s to %s\n", name, tagRemote)
			}
			return nil
		}

		if _, err := gittag.EnsureAtHead(repoPath, name, message, tagSign); err != nil {
			if errors.Is(err, gittag.ErrTagConflict) {
				return fmt.Errorf("tag %q conflicts with an existing tag created concurrently", name)
			}
			return err
		}
		fmt.Println(name)

		if tagPush {
			if err := gittag.Push(repoPath, tagRemote, name); err != nil {
				return fmt.Errorf("push tag: %w", err)
			}
		}
		return nil
	},
}

func init() {
	tagCmd.Flags().BoolVar(&tagPush, "push", false, "push the tag to the remote after creating it")
	tagCmd.Flags().StringVar(&tagRemote, "remote", "origin", "remote to push the tag to (with --push)")
	tagCmd.Flags().BoolVar(&tagDryRun, "dry-run", false, "print what would be done without mutating anything")
	tagCmd.Flags().BoolVar(&tagForce, "force", false, "tag even if the worktree is dirty")
	tagCmd.Flags().StringVarP(&tagMessage, "message", "m", "", "annotated-tag message (default: \"Release v<semver>\")")
	tagCmd.Flags().BoolVar(&tagSign, "sign", false, "GPG-sign the tag (uses git's gpg config)")
	tagCmd.Flags().StringVar(&tagInitial, "initial", "", "create the first release tag verbatim; only valid when no semver-shaped tag exists yet")
	rootCmd.AddCommand(tagCmd)
}
