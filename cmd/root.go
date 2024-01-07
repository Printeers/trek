package cmd

import (
	"github.com/spf13/cobra"

	"github.com/printeers/trek/internal"
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "trek",
		Short: "A tool to create, organize and run database migrations.",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			internal.InitializeFlags(cmd)
		},
	}

	rootCmd.AddCommand(NewApplyCommand())
	rootCmd.AddCommand(NewCheckCommand())
	rootCmd.AddCommand(NewGenerateCommand())
	rootCmd.AddCommand(NewInitCommand())

	return rootCmd
}
