package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dmallubhotla/hanko/internal/config"
	"github.com/dmallubhotla/hanko/internal/gitinfo"
	"github.com/dmallubhotla/hanko/internal/gittag"
	"github.com/dmallubhotla/hanko/internal/version"
	"github.com/spf13/cobra"
)

// sentinel value pflag stores when `--initial` is passed without `=value`.
// Picked to be unrepresentable as a real user value.
const sealInitialFromConfig = "\x00from-config"

var (
	sealDryRun  bool
	sealBump    string
	sealInitial string
)

var sealCmd = &cobra.Command{
	Use:     "seal",
	Short:   "Run a release: pre-flight, run pre-commit hooks, commit, tag, push",
	GroupID: "stamp",
	Long: `Bundles the release-time rite into one invocation:

Resolve version, stamp repo, commit changes and atomically tag-and-push.
` + "`--dry-run`" + ` walks the pipeline without mutating; prints what each step would do.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch sealBump {
		case "", "patch", "minor", "major", "none":
		default:
			return fmt.Errorf("unknown --bump %q (want: patch, minor, major, none)", sealBump)
		}

		useInitial := cmd.Flags().Changed("initial")
		if useInitial && sealBump != "" {
			return fmt.Errorf("--initial and --bump are mutually exclusive")
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

		// Resolve --initial up front so we can validate before doing work.
		// Bootstrap-only, same constraints as `hanko tag --initial`.
		var initialValue string
		if useInitial {
			if sealInitial == sealInitialFromConfig {
				initialValue = cfg.InitialVersion
			} else {
				initialValue = sealInitial
			}
			if info.LatestTag != "" {
				return fmt.Errorf("--initial only valid when no semver-shaped tag exists (found %q)", info.LatestTag)
			}
			if !initialRE.MatchString(initialValue) {
				return fmt.Errorf("--initial %q is not a semver-shaped tag (want v?MAJOR.MINOR.PATCH[-pre][+meta])", initialValue)
			}
		}

		v, err := version.Compute(info, cfg, sealBump)
		if err != nil {
			return fmt.Errorf("compute version: %w", err)
		}

		// Override the computed version with the verbatim initial value.
		// In the bootstrap state, Compute returns a prerelease (e.g.
		// `0.1.0-main.1`); --initial replaces that with the clean value the
		// caller named. The prerelease blocker below stays in place: if the
		// caller asks for a prerelease initial (e.g. `v0.1.0-beta.1`), it
		// will correctly refuse.
		if useInitial {
			overrideVersionForInitial(&v, initialValue)
		}

		if v.IsPreRelease && refusePrereleaseSeal(cfg) {
			return fmt.Errorf("computed version %q is a pre-release; seal refuses by default (set seal.refuse-prerelease: false to override)", v.SemVer)
		}

		commitMessage := v.Expand(cfg.Seal.CommitMessage)

		// Tag name: verbatim from --initial (caller picks the v-prefix
		// policy), otherwise D-002 (mirror the latest tag's `v` prefix).
		var tagName string
		if useInitial {
			tagName = initialValue
		} else {
			prefix := ""
			if strings.HasPrefix(info.LatestTag, "v") {
				prefix = "v"
			}
			tagName = prefix + v.SemVer
		}

		// No-release-needed: the bump strategy decided the next tag should
		// have the same name as the latest existing one. Could be:
		//   - re-running seal on a tagged commit (tag is at HEAD already)
		//   - strict-conventional + an all-chore range (no signal → no bump)
		//   - a branch with `increment: none` and no other signal
		// All three are "your worktree is up to date; nothing to release."
		if info.LatestTag != "" && tagName == info.LatestTag {
			fmt.Printf("no release needed: computed tag %s matches the latest existing tag\n", tagName)
			if d := v.Decision; d.Strategy == "conventional-commits" && len(d.Commits) > 0 && d.StrongestSignal == "none" {
				fmt.Printf("(%d commit(s) since %s, none with feat:/fix:/feat!: signals)\n", len(d.Commits), d.BaseTag)
			}
			return nil
		}

		// Refuse if the tag already exists at any commit. The "match the
		// latest tag" case is handled above; this catches stale tags from a
		// prior aborted seal at a different commit.
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

// overrideVersionForInitial rewrites v in place so it represents the verbatim
// `--initial` value: major/minor/patch parsed from the value, no pre-release
// (unless the value itself encoded one), no synthesised build metadata.
// Caller must have already validated `value` against initialRE.
func overrideVersionForInitial(v *version.Version, value string) {
	s := strings.TrimPrefix(value, "v")
	core, build := s, ""
	if i := strings.Index(s, "+"); i >= 0 {
		core, build = s[:i], s[i+1:]
	}
	mmp, pre := core, ""
	if i := strings.Index(core, "-"); i >= 0 {
		mmp, pre = core[:i], core[i+1:]
	}
	parts := strings.SplitN(mmp, ".", 3)
	v.Major, _ = strconv.Atoi(parts[0])
	v.Minor, _ = strconv.Atoi(parts[1])
	v.Patch, _ = strconv.Atoi(parts[2])
	v.PreRelease = pre
	v.BuildMetadata = build
	v.IsPreRelease = pre != ""
	v.SemVer = s
	v.FullSemVer = s
	if build != "" {
		v.FullSemVer = core + "+" + build
	}
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
	sealCmd.Flags().StringVar(&sealInitial, "initial", "", "seal the first release verbatim; pass `--initial` alone to use `initial-version` from config, or `--initial=<value>` to override. Only valid when no semver-shaped tag exists yet.")
	sealCmd.Flags().Lookup("initial").NoOptDefVal = sealInitialFromConfig
	rootCmd.AddCommand(sealCmd)
}
