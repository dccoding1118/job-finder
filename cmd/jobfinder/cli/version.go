package cli

import (
	"fmt"
	goruntime "runtime"

	"github.com/dccoding1118/job-finder/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the jobfinder version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "jobfinder %s %s/%s\n",
				version.Current(), goruntime.GOOS, goruntime.GOARCH)
			return err
		},
	}
}
