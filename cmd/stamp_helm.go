package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var (
	stampHelmDryRun bool
)

var stampHelmCmd = &cobra.Command{
	Use:   "helm <chart-dir>",
	Short: "Set version and appVersion in a chart's Chart.yaml",
	Long: `Edits the top-level ` + "`version`" + ` and ` + "`appVersion`" + ` keys in
` + "`<chart-dir>/Chart.yaml`" + ` in place.

The edit is line-based rather than a YAML round-trip: comments, key order,
and incidental whitespace survive untouched. The cost is strictness — the
file must contain both keys as top-level scalars, written one per line.
That's the canonical shape; anything fancier is unusual enough that hanko
prefers to refuse rather than guess.

` + "`--dry-run`" + ` prints the unified-diff-ish before/after instead of writing.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		chart := args[0]
		path := filepath.Join(chart, "Chart.yaml")

		v, err := resolveVersion()
		if err != nil {
			return err
		}

		orig, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		updated, changes, err := setChartVersions(orig, v.SemVer)
		if err != nil {
			return err
		}

		if stampHelmDryRun {
			for _, c := range changes {
				fmt.Println(c)
			}
			return nil
		}
		if !bytes.Equal(orig, updated) {
			if err := os.WriteFile(path, updated, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
		}
		for _, c := range changes {
			fmt.Println(c)
		}
		return nil
	},
}

// chartVersionLineRE matches `version:` or `appVersion:` lines: optional
// quotes around the value, optional trailing comment. The captured groups
// let us preserve the prefix and any trailing comment when rewriting.
var chartVersionLineRE = regexp.MustCompile(`^(version|appVersion)(\s*:\s*)("[^"]*"|'[^']*'|[^\s#]*)(\s*(?:#.*)?)\s*$`)

// setChartVersions rewrites `version:` and `appVersion:` top-level keys to
// the given semver. Returns the rewritten file plus a human-readable list
// of "key: old → new" change descriptions, in order of appearance.
func setChartVersions(content []byte, semver string) ([]byte, []string, error) {
	lines := strings.Split(string(content), "\n")
	var changes []string
	seen := map[string]bool{}

	for i, line := range lines {
		m := chartVersionLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, sep, val, trailing := m[1], m[2], m[3], m[4]
		oldVal := strings.Trim(val, `"'`)

		// appVersion is conventionally quoted to keep it a string even when
		// it looks numeric; `version` is bare. Preserve whichever form the
		// file already uses, defaulting per Helm convention if missing.
		newVal := semver
		switch {
		case strings.HasPrefix(val, `"`):
			newVal = `"` + semver + `"`
		case strings.HasPrefix(val, `'`):
			newVal = `'` + semver + `'`
		case key == "appVersion":
			newVal = `"` + semver + `"`
		}

		lines[i] = key + sep + newVal + trailing
		changes = append(changes, fmt.Sprintf("%s: %s → %s", key, oldVal, semver))
		seen[key] = true
	}

	if !seen["version"] {
		return nil, nil, fmt.Errorf("no top-level `version:` key found in Chart.yaml")
	}
	if !seen["appVersion"] {
		return nil, nil, fmt.Errorf("no top-level `appVersion:` key found in Chart.yaml")
	}

	return []byte(strings.Join(lines, "\n")), changes, nil
}

func init() {
	stampHelmCmd.Flags().BoolVar(&stampHelmDryRun, "dry-run", false, "print the changes that would be made without writing")
	stampCmd.AddCommand(stampHelmCmd)
}
