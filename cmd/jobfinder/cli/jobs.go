package cli

import (
	"fmt"

	"github.com/dccoding1118/job-finder/internal/store"
	"github.com/spf13/cobra"
)

func newJobsCmd() *cobra.Command {
	var dbPath, processState, applyState, source string
	cmd := &cobra.Command{Use: "jobs", Short: "List locally stored jobs", RunE: func(cmd *cobra.Command, _ []string) error {
		data, err := store.Open(dbPath)
		if err != nil {
			return err
		}
		defer func() { _ = data.Close() }()
		jobs, err := data.ListJobs(cmd.Context(), store.JobFilter{ProcessState: processState, ApplyState: applyState, Source: source}, store.JobSortScore)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%s\t%s\t%s\n", job.ID, job.ProcessState, job.Title, job.CompanyName, job.URL); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().StringVar(&dbPath, "db", ".local-dev/jobfinder.db", "path to SQLite database")
	cmd.Flags().StringVar(&processState, "process-state", "", "filter by process state")
	cmd.Flags().StringVar(&applyState, "apply-state", "", "filter by application state")
	cmd.Flags().StringVar(&source, "source", "", "filter by source")
	return cmd
}
