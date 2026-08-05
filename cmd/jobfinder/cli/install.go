package cli

import (
	"fmt"

	"github.com/dccoding1118/job-finder/internal/install"
	"github.com/dccoding1118/job-finder/internal/paths"
	"github.com/spf13/cobra"
)

// newInstallCmds carries the whole meaning of deployment: install, update and
// rollback are one Go implementation shared by Linux and Windows, so the
// bootstrap scripts only have to fetch an artifact and verify its checksum.
//
// The executable running these commands is the installation medium. It copies
// itself to the resident location the scheduler executes, which is why the
// download directory can be deleted afterwards.
func newInstallCmds() []*cobra.Command {
	command := func(use, short, long string, action func(*cobra.Command, install.Options) error) *cobra.Command {
		var opts install.Options
		cmd := &cobra.Command{Use: use, Short: short, Long: long, RunE: func(cmd *cobra.Command, _ []string) error {
			opts.Out = cmd.OutOrStdout()
			return action(cmd, opts)
		}}
		cmd.Flags().StringVar(&opts.AssetDir, "assets", "",
			"directory holding the unpacked artifact's configs/ and scheduling templates (default: next to this executable)")
		cmd.Flags().BoolVar(&opts.SkipVerify, "skip-verify", false,
			"skip the effect-surface checks on the running service; for environments with no user session")
		return cmd
	}
	return []*cobra.Command{
		command("install", "Install jobfinder into this user's environment",
			"Provision the configuration and data locations, place the resident binary, mount the platform's\n"+
				"scheduling mechanism, and verify the result on the running process. An existing configuration,\n"+
				"profile, denylist or database is never overwritten.",
			func(cmd *cobra.Command, opts install.Options) error { return install.Install(cmd.Context(), opts) }),
		command("update", "Replace the installed binary and scheduling definitions with this artifact's",
			"Keep the outgoing binary for rollback, replace the resident copy and the scheduling definitions,\n"+
				"restart the API and verify that the process now running is the new binary. Configuration and\n"+
				"data are untouched.",
			func(cmd *cobra.Command, opts install.Options) error { return install.Update(cmd.Context(), opts) }),
		command("rollback", "Restore the previously installed binary and scheduling definitions",
			"Restore what the last install or update kept aside and verify the restored version is the one\n"+
				"running. The database is never touched: restore it from the backup directory only when\n"+
				"corruption is confirmed.",
			func(cmd *cobra.Command, opts install.Options) error { return install.Rollback(cmd.Context(), opts) }),
		newPathsCmd(),
	}
}

// newPathsCmd answers "which files is this install actually using" from the
// program itself, so diagnosing a wrong-config problem does not depend on
// knowing each platform's layout by heart.
func newPathsCmd() *cobra.Command {
	return &cobra.Command{Use: "paths", Short: "Print the locations this platform uses", RunE: func(cmd *cobra.Command, _ []string) error {
		layout, err := paths.Resolve()
		if err != nil {
			return err
		}
		for _, row := range layout.Rows() {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-14s %s\n", row[0], row[1]); err != nil {
				return err
			}
		}
		return nil
	}}
}
