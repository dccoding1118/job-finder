package cli

import (
	"fmt"

	"github.com/dccoding1118/job-finder/internal/paths"
	"github.com/spf13/cobra"
)

// newLetterCmd is the CLI counterpart of the dashboard's generate entry: a
// letter is only ever drafted after the user asks for one.
func newLetterCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "letter", Short: "Request letters for recommended jobs"}
	cmd.AddCommand(newLetterRequestCmd())
	return cmd
}

func newLetterRequestCmd() *cobra.Command {
	var path string
	var jobID int64
	cmd := &cobra.Command{Use: "request", Short: "Request a letter for one recommended job", RunE: func(cmd *cobra.Command, _ []string) error {
		if jobID <= 0 {
			return fmt.Errorf("--job must be a positive job id")
		}
		rt, err := loadRuntime(path)
		if err != nil {
			return err
		}
		defer rt.close()
		if err := rt.pipeline.RequestLetter(cmd.Context(), jobID); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "requested: %d\n", jobID)
		return nil
	}}
	cmd.Flags().StringVar(&path, "config", paths.DefaultConfig(), "path to config.yaml")
	cmd.Flags().Int64Var(&jobID, "job", 0, "job id to request a letter for")
	return cmd
}
