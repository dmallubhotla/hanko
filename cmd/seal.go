package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dmallubhotla/hanko/internal/config"
	"github.com/dmallubhotla/hanko/internal/gitinfo"
	"github.com/dmallubhotla/hanko/internal/gittag"
	"github.com/dmallubhotla/hanko/internal/version"
	"github.com/spf13/cobra"
)

var (
	sealDryRun bool
	sealBump   string
)

var sealCmd = &cobra.Command{
	Use:     "seal",
	Short:   "Run a release: pre-flight, run pre-commit hooks, commit, tag, push",
	GroupID: "stamp",
	Long: `Bundles the release-time rite into one invocation:

  1. Pre-flight: refuse if the worktree is dirty (so the release commit
     contains only what hanko + the hooks produced).
  2. Resolve version. Refuse if it's a pre-release (unless
     seal.refuse-prerelease is false).
  3. Run seal.pre-commit hooks in order, in the repo root. Each is a shell
     command with template-expanded args ({semver}, {full}, {major}, etc.).
     Non-zero exit aborts; worktree is left as-is.
  4. Create a single commit with everything staged-or-modified, using
     seal.commit-message as the message (also template-expanded).
  5. Create an annotated tag matching what hanko tag would produce.
  6. Push commit + tag atomically to seal.push-remote (default origin).

` + "`--dry-run`" + ` walks the pipeline without mutating; prints what each step would do.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch sealBump {
		case "", "patch", "minor", "major", "none":
		default:
			return fmt.Errorf("unknown --bump %q (want: patch, minor, major, none)", sealBump)
		}

		cfg, err := config.Load(repoPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		info, err := gitinfo.Read(repoPath, cfg.TagMatch)
		if err != nil {
			return fmt.Errorf("read git info: %w", err)
		}
		if info.Shallow {
			return ErrShallow
		}
		if info.InProgress != "" {
			return errInProgress(info.InProgress)
		}

		// Pre-flight: dirty worktree means the release commit would contain
		// pre-existing dirt. Hooks introduce dirt by design; that's fine —
		// but the worktree must be clean going *in*.
		if info.Dirty {
			return fmt.Errorf("worktree is dirty before seal; commit or stash first")
		}
		if info.Detached {
			return fmt.Errorf("detached HEAD; seal needs a branch to push to")
		}

		v, err := version.Compute(info, cfg, sealBump)
		if err != nil {
			return fmt.Errorf("compute version: %w", err)
		}

		if v.IsPreRelease && refusePrereleaseSeal(cfg) {
			return fmt.Errorf("computed version %q is a pre-release; seal refuses by default (set seal.refuse-prerelease: false to override)", v.SemVer)
		}

		commitMessage := v.Expand(cfg.Seal.CommitMessage)

		// Tag name follows D-002: mirror the latest tag's `v` prefix.
		prefix := ""
		if strings.HasPrefix(info.LatestTag, "v") {
			prefix = "v"
		}
		tagName := prefix + v.SemVer

		// Refuse if the tag already exists at a different commit. The "at
		// HEAD" case can't legitimately fire here (we're about to make a new
		// commit), but a stale tag from a prior aborted seal would.
		if exists, err := gittag.Exists(repoPath, tagName); err != nil {
			return fmt.Errorf("check tag exists: %w", err)
		} else if exists {
			return fmt.Errorf("tag %q already exists; clean up before sealing", tagName)
		}

		if sealDryRun {
			fmt.Printf("seal plan:\n")
			fmt.Printf("  version:        %s\n", v.SemVer)
			fmt.Printf("  branch:         %s\n", info.Branch)
			fmt.Printf("  tag name:       %s\n", tagName)
			fmt.Printf("  commit message: %s\n", commitMessage)
			fmt.Printf("  stamp-targets (%d):\n", len(cfg.StampTargets))
			for _, t := range cfg.StampTargets {
				fmt.Printf("    - %s (%s)\n", t.Path, t.Format)
			}
			fmt.Printf("  pre-commit hooks (%d):\n", len(cfg.Seal.PreCommit))
			for _, h := range cfg.Seal.PreCommit {
				fmt.Printf("    - %s\n", v.Expand(h))
			}
			if remote := pushRemote(cfg); remote != "" {
				fmt.Printf("  push to:        %s\n", remote)
			} else {
				fmt.Printf("  push:           (disabled)\n")
			}
			return nil
		}

		// Apply declarative stamp-targets (if any) before user hooks.
		// Stamper failures abort the seal with the worktree untouched.
		if len(cfg.StampTargets) > 0 {
			if err := applyStampTargets(cfg.StampTargets, v, false); err != nil {
				return fmt.Errorf("stamp-targets: %w", err)
			}
		}

		// Run hooks. Stdout/stderr forwarded so the user sees what their
		// tools are doing.
		for _, hook := range cfg.Seal.PreCommit {
			expanded := v.Expand(hook)
			fmt.Fprintf(os.Stderr, "+ %s\n", expanded)
			c := exec.Command("sh", "-c", expanded)
			c.Dir = repoPath
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("pre-commit hook failed: %w", err)
			}
		}

		// Stage everything the hooks (and any prior `hanko stamp`) produced.
		if err := runGit(repoPath, "add", "-A"); err != nil {
			return fmt.Errorf("git add: %w", err)
		}
		// If nothing changed (no hooks, no targets, nothing to stamp), the
		// commit step would fail. Detect that and skip ahead to tag+push so
		// seal is idempotent on a tagged release commit.
		hasChanges, err := indexHasChanges(repoPath)
		if err != nil {
			return fmt.Errorf("check index: %w", err)
		}
		if hasChanges {
			if err := runGit(repoPath, "commit", "-m", commitMessage); err != nil {
				return fmt.Errorf("git commit: %w", err)
			}
		}

		if _, err := gittag.EnsureAtHead(repoPath, tagName, commitMessage, false); err != nil {
			if errors.Is(err, gittag.ErrTagConflict) {
				return fmt.Errorf("tag %q conflicts with an existing tag created concurrently", tagName)
			}
			return fmt.Errorf("tag: %w", err)
		}
		fmt.Println(tagName)

		if remote := pushRemote(cfg); remote != "" {
			if err := runGit(repoPath, "push", "--atomic", remote, info.Branch, "refs/tags/"+tagName); err != nil {
				return fmt.Errorf("push: %w", err)
			}
		}
		return nil
	},
}

func refusePrereleaseSeal(cfg *config.Config) bool {
	if cfg.Seal.RefusePrerelease == nil {
		return true
	}
	return *cfg.Seal.RefusePrerelease
}

func pushRemote(cfg *config.Config) string {
	if cfg.Seal.PushRemote == nil {
		return ""
	}
	return *cfg.Seal.PushRemote
}

func runGit(repo string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = repo
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func indexHasChanges(repo string) (bool, error) {
	c := exec.Command("git", "diff", "--cached", "--quiet")
	c.Dir = repo
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func init() {
	sealCmd.Flags().BoolVar(&sealDryRun, "dry-run", false, "print the planned steps without mutating anything")
	sealCmd.Flags().StringVar(&sealBump, "bump", "", "force a bump direction for this invocation: patch | minor | major | none")
	rootCmd.AddCommand(sealCmd)
}
