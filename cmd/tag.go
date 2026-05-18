package cmd

import (
	"errors"
	"fmt"

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
)

var tagCmd = &cobra.Command{
	Use:     "tag",
	Short:   "Create a git tag for the computed version",
	GroupID: "stamp",
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := resolveInfo()
		if err != nil {
			return err
		}
		v, err := version.Compute(info)
		if err != nil {
			return fmt.Errorf("compute version: %w", err)
		}

		name := "v" + v.SemVer

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

		if info.Dirty && !tagForce {
			return fmt.Errorf("worktree is dirty; refusing to tag (pass --force to override)")
		}
		// D-011: hanko tag never creates pre-release tags.
		// Pre-release versions live on feature / hotfix branches; the canonical "release" tag happens after merge to mainline.
		// If you genuinely need a pre-release marker, create it by hand with `git tag`.
		if v.IsPreRelease {
			return fmt.Errorf("computed version %q is a pre-release; `hanko tag` only tags releases (merge to main/master first)", v.SemVer)
		}

		exists, err := gittag.Exists(repoPath, name)
		if err != nil {
			return fmt.Errorf("check tag exists: %w", err)
		}
		if exists {
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
	rootCmd.AddCommand(tagCmd)
}
