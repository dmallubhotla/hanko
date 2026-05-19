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

var stampNixDryRun bool

var stampNixCmd = &cobra.Command{
	Use:   "nix [flake-file]",
	Short: "Set the version attr in a flake.nix to the computed semver",
	Long: `Edits the first ` + "`version = \"...\";`" + ` attribute in ` + "`flake.nix`" + `
(or the supplied path) in place. Intended as a release-time companion to
` + "`hanko tag`" + `: bump the nix derivation's version when cutting a release,
mirroring the new git tag.

The edit is line-based and preserves comments and ordering. The file must
contain at least one ` + "`version = \"<value>\";`" + ` attr; the first match wins.

` + "`--dry-run`" + ` prints the change that would be made without writing.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "flake.nix"
		if len(args) == 1 {
			path = args[0]
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoPath, path)
		}

		v, err := resolveVersion("")
		if err != nil {
			return err
		}

		orig, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		updated, changes, err := setNixVersion(orig, v.SemVer)
		if err != nil {
			return err
		}

		if stampNixDryRun {
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

// nixVersionLineRE matches a `version = "..."` nix attr line: leading
// whitespace, double-quoted value, mandatory semicolon, optional trailing
// comment. The captured groups let us preserve everything around the value.
var nixVersionLineRE = regexp.MustCompile(`^(\s*version\s*=\s*)"([^"]*)"(\s*;\s*(?:#.*)?)\s*$`)

// setNixVersion rewrites every `version = "..."` line that shares the same
// current value. If multiple lines exist with different values, refuses —
// that's the multi-product flake case and the caller has to disambiguate
// (D-015).
//
// Assumption: when several `version = "X"` lines share the same value, they
// all refer to the same release. False-positive case: a coincidental match
// like a vendored-package override at the same version string would be
// rewritten too. Document this assumption in the schema reference; the
// divergence-refusal covers the obvious bad shape.
//
// Returns the rewritten file plus a "version: old → new" change description
// (one entry, since by definition all values were equal).
func setNixVersion(content []byte, semver string) ([]byte, []string, error) {
	lines := strings.Split(string(content), "\n")

	type hit struct {
		lineIdx               int
		prefix, oldVal, trail string
	}
	var hits []hit
	for i, line := range lines {
		m := nixVersionLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		hits = append(hits, hit{i, m[1], m[2], m[3]})
	}

	if len(hits) == 0 {
		return nil, nil, fmt.Errorf("no `version = \"...\";` attr found")
	}

	// Reject divergent values up front.
	for _, h := range hits[1:] {
		if h.oldVal != hits[0].oldVal {
			return nil, nil, fmt.Errorf(
				"multiple `version = \"...\";` attrs with different values (%q vs %q); "+
					"hoist to a shared `let version = \"…\";` binding and `inherit version` into each derivation",
				hits[0].oldVal, h.oldVal,
			)
		}
	}

	for _, h := range hits {
		lines[h.lineIdx] = h.prefix + `"` + semver + `"` + h.trail
	}
	return []byte(strings.Join(lines, "\n")),
		[]string{fmt.Sprintf("version: %s → %s (%d line%s)", hits[0].oldVal, semver, len(hits), pluralS(len(hits)))},
		nil
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func init() {
	stampNixCmd.Flags().BoolVar(&stampNixDryRun, "dry-run", false, "print the change that would be made without writing")
	stampCmd.AddCommand(stampNixCmd)
}
