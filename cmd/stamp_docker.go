package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/dmallubhotla/hanko/internal/version"
	"github.com/spf13/cobra"
)

var stampDockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Emit container-image tags and OCI labels for the computed version",
	Long: `Two subcommands:

  hanko stamp docker tags <image>     # expand version into a list of full
                                      # image references to push
  hanko stamp docker labels           # emit org.opencontainers.image.* labels

Both take their version from the same source as ` + "`hanko version`" + `.`,
}

// ── tags ──────────────────────────────────────────────────────────────────

var (
	stampDockerTagsLatest    bool
	stampDockerTagsBranchSha bool
	stampDockerTagsExtra     []string
)

var stampDockerTagsCmd = &cobra.Command{
	Use:   "tags <image>",
	Short: "Expand the computed version into a list of <image>:<tag> refs",
	Long: `Emits one full image reference per line. Suitable for piping into
` + "`xargs -I{} podman push {}` or similar." + `

Default fan-out for a non-prerelease semver on the default branch:

    <image>:<full>
    <image>:<major>.<minor>
    <image>:<major>
    <image>:latest

For a pre-release semver, only ` + "`<image>:<full>`" + ` is emitted — fan-out
to moving tags would tag movement to an unstable build.

` + "`--branch-sha-tag`" + ` (default true) additionally emits
` + "`<image>:<branch>-<short-sha>`" + `.
` + "`--extra`" + ` appends raw tags after the computed ones; repeatable.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		image := strings.TrimRight(args[0], ":/")
		v, err := resolveVersion()
		if err != nil {
			return err
		}
		for _, tag := range computeDockerTags(v, stampDockerTagsLatest, stampDockerTagsBranchSha, stampDockerTagsExtra) {
			fmt.Printf("%s:%s\n", image, tag)
		}
		return nil
	},
}

// computeDockerTags returns the list of tag suffixes (without the image
// prefix) implied by v and the caller's policy. Pure function so the test
// can hit it directly.
func computeDockerTags(v version.Version, latest, branchSha bool, extras []string) []string {
	var tags []string
	tags = append(tags, v.SemVer)

	if !v.IsPreRelease {
		tags = append(tags,
			fmt.Sprintf("%d.%d", v.Major, v.Minor),
			fmt.Sprintf("%d", v.Major),
		)
		if latest && isMainline(v.BranchName) {
			tags = append(tags, "latest")
		}
	}

	if branchSha && v.BranchName != "" && v.ShortSha != "" {
		tags = append(tags, fmt.Sprintf("%s-%s", sanitizeForTag(v.BranchName), v.ShortSha))
	}

	for _, e := range extras {
		e = strings.TrimSpace(e)
		if e != "" {
			tags = append(tags, e)
		}
	}
	return tags
}

func isMainline(b string) bool { return b == "main" || b == "master" }

// sanitizeForTag mirrors version.sanitizeBranch closely enough for container
// tags; duplicated here to avoid a cross-package internal coupling.
func sanitizeForTag(b string) string {
	out := make([]byte, 0, len(b))
	prevDash := false
	for _, r := range strings.ToLower(b) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out = append(out, byte(r))
			prevDash = false
		default:
			if !prevDash {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(string(out), "-")
	if s == "" {
		return "branch"
	}
	return s
}

// ── labels ────────────────────────────────────────────────────────────────

var (
	stampDockerLabelsOutput string
	stampDockerLabelsFile   string
	stampDockerLabelsSource string
	stampDockerLabelsTitle  string
)

var stampDockerLabelsCmd = &cobra.Command{
	Use:   "labels",
	Short: "Emit org.opencontainers.image.* labels for the computed version",
	Long: `Output modes:

  --output args  (default) — emit one ` + "`--label key=value`" + ` per line,
                              ready to xargs into ` + "`docker build`" + `
  --output file  --file PATH — write a label-file (key=value per line)
                                suitable for ` + "`docker build --label-file`" + `

Always sets ` + "`version`, `revision`, `created`" + `. Pass ` + "`--source`" + ` and
` + "`--title`" + ` to set the matching labels; absent values are omitted.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := resolveVersion()
		if err != nil {
			return err
		}
		pairs := dockerLabels(v, stampDockerLabelsSource, stampDockerLabelsTitle)

		switch stampDockerLabelsOutput {
		case "args":
			for _, p := range pairs {
				fmt.Printf("--label %s\n", p)
			}
		case "file":
			if stampDockerLabelsFile == "" {
				return fmt.Errorf("--output file requires --file PATH")
			}
			content := strings.Join(pairs, "\n") + "\n"
			if err := os.WriteFile(stampDockerLabelsFile, []byte(content), 0o644); err != nil {
				return fmt.Errorf("write label file: %w", err)
			}
		default:
			return fmt.Errorf("unknown --output %q (want: args, file)", stampDockerLabelsOutput)
		}
		return nil
	},
}

func dockerLabels(v version.Version, source, title string) []string {
	pairs := []string{
		fmt.Sprintf("org.opencontainers.image.version=%s", v.SemVer),
		fmt.Sprintf("org.opencontainers.image.revision=%s", v.Sha),
	}
	if v.CommitDate != "" {
		pairs = append(pairs, fmt.Sprintf("org.opencontainers.image.created=%s", v.CommitDate))
	}
	if source != "" {
		pairs = append(pairs, fmt.Sprintf("org.opencontainers.image.source=%s", source))
	}
	if title != "" {
		pairs = append(pairs, fmt.Sprintf("org.opencontainers.image.title=%s", title))
	}
	return pairs
}

func init() {
	stampDockerTagsCmd.Flags().BoolVar(&stampDockerTagsLatest, "latest-on-default-branch", true, "emit :latest when on main/master and non-prerelease")
	stampDockerTagsCmd.Flags().BoolVar(&stampDockerTagsBranchSha, "branch-sha-tag", true, "emit :<branch>-<short-sha>")
	stampDockerTagsCmd.Flags().StringArrayVar(&stampDockerTagsExtra, "extra", nil, "extra raw tag to append (repeatable)")
	stampDockerCmd.AddCommand(stampDockerTagsCmd)

	stampDockerLabelsCmd.Flags().StringVar(&stampDockerLabelsOutput, "output", "args", "output mode: args | file")
	stampDockerLabelsCmd.Flags().StringVar(&stampDockerLabelsFile, "file", "", "destination path for --output file")
	stampDockerLabelsCmd.Flags().StringVar(&stampDockerLabelsSource, "source", "", "value for org.opencontainers.image.source (omitted if empty)")
	stampDockerLabelsCmd.Flags().StringVar(&stampDockerLabelsTitle, "title", "", "value for org.opencontainers.image.title (omitted if empty)")
	stampDockerCmd.AddCommand(stampDockerLabelsCmd)

	stampCmd.AddCommand(stampDockerCmd)
}
