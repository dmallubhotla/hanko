package cmd

import "github.com/spf13/cobra"

var stampCmd = &cobra.Command{
	Use:     "stamp",
	Short:   "Stamp computed version/labels onto a build artifact",
	GroupID: "stamp",
}

func init() {
	rootCmd.AddCommand(stampCmd)
}
