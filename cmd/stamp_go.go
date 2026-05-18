package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	stampGoPackage string
)

var stampGoCmd = &cobra.Command{
	Use:   "go-ldflags",
	Short: "Emit -ldflags for stamping a Go binary at build time",
	Long: `Emit a single line of -X flags suitable for splicing into
` + "`go build -ldflags \"$(hanko stamp go-ldflags)\" ./...`" + `.

By default stamps three variables on package "main": version (full SemVer),
commit (full SHA), date (committer date of HEAD). Pass --package to stamp a
different import path.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := resolveVersion()
		if err != nil {
			return err
		}
		pkg := stampGoPackage
		parts := []string{
			fmt.Sprintf("-X %s.version=%s", pkg, v.SemVer),
			fmt.Sprintf("-X %s.commit=%s", pkg, v.Sha),
			fmt.Sprintf("-X %s.date=%s", pkg, v.CommitDate),
		}
		fmt.Println(strings.Join(parts, " "))
		return nil
	},
}

func init() {
	stampGoCmd.Flags().StringVar(&stampGoPackage, "package", "main", "Go import path of the package whose variables get stamped")
	stampCmd.AddCommand(stampGoCmd)
}
