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

		v, err := resolveVersion()
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

// setNixVersion rewrites the first `version = "..."` line found. Returns the
// rewritten file plus a "version: old → new" change description.
//
// First-match-wins is intentional: real flakes can have several string attrs
// that look like version assignments (inputs metadata, sibling packages),
// but the package's own `version` is overwhelmingly the first one in the
// `buildGoApplication`/`mkDerivation` block at the top.
func setNixVersion(content []byte, semver string) ([]byte, []string, error) {
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		m := nixVersionLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		prefix, oldVal, trailing := m[1], m[2], m[3]
		lines[i] = prefix + `"` + semver + `"` + trailing
		return []byte(strings.Join(lines, "\n")),
			[]string{fmt.Sprintf("version: %s → %s", oldVal, semver)},
			nil
	}
	return nil, nil, fmt.Errorf("no `version = \"...\";` attr found")
}

func init() {
	stampNixCmd.Flags().BoolVar(&stampNixDryRun, "dry-run", false, "print the change that would be made without writing")
	stampCmd.AddCommand(stampNixCmd)
}
