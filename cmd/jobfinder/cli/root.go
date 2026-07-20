package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

// ExitError represents a CLI error with an explicit process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

// Execute constructs and runs the jobfinder command tree.
func Execute(ctx context.Context) error {
	rootCmd := newRootCmd()
	rootCmd.SetContext(ctx)

	return rootCmd.Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "jobfinder",
		Short:         "Find and match jobs from configured sources",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("a subcommand is required")
		},
	}
	root.AddCommand(newProfileCmd(), newJobsCmd(), newRunCmd(), newLetterCmd(), newServeCmd(), newVerifyCmd())
	return root
}
