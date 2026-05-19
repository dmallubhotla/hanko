package cmd

import (
	"fmt"
	"os"

	"github.com/dmallubhotla/hanko/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configCmd = &cobra.Command{
	Use:     "config",
	Short:   "Show configuration details",
	GroupID: "compute",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the resolved (merged-with-defaults) configuration as YAML",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(repoPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		out, err := yaml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal config: %w", err)
		}
		fmt.Print(string(out))
		return nil
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the loaded .hanko.yaml path, or empty if defaults are in use",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(repoPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if cfg.Source == "" {
			fmt.Fprintln(os.Stderr, "(no .hanko.yaml found; defaults in use)")
			return nil
		}
		fmt.Println(cfg.Source)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configPathCmd)
	rootCmd.AddCommand(configCmd)
}
