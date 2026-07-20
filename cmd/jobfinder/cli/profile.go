package cli

import (
	"fmt"
	"path/filepath"

	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/spf13/cobra"
)

const (
	defaultProfilePath  = ".local-dev/profile.yaml"
	defaultDenylistPath = ".local-dev/pii-denylist.txt"
)

func newProfileCmd() *cobra.Command {
	var profilePath, denylistPath string
	cmd := &cobra.Command{Use: "profile", Short: "Validate and inspect the anonymous profile"}
	cmd.PersistentFlags().StringVar(&profilePath, "profile", defaultProfilePath, "path to profile.yaml")
	cmd.PersistentFlags().StringVar(&denylistPath, "denylist", defaultDenylistPath, "path to pii-denylist.txt")
	cmd.AddCommand(
		&cobra.Command{Use: "lint", Short: "Validate profile structure and scan for PII", RunE: func(cmd *cobra.Command, _ []string) error {
			_, contents, err := profile.Load(profilePath)
			if err != nil {
				return err
			}
			denylist, err := profile.LoadDenylist(denylistPath)
			if err != nil {
				return err
			}
			if lintErr := profile.LintText(contents, denylist); lintErr != nil {
				return lintErr
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "profile lint passed")
			return err
		}},
		&cobra.Command{Use: "show", Short: "Show the loaded profile summary", RunE: func(cmd *cobra.Command, _ []string) error {
			value, _, err := profile.Load(profilePath)
			if err != nil {
				return err
			}
			summary := value.SummaryView()
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "years_of_experience: %d\neducation: %s (%s)\nexpert: %v\nproficient: %v\nfamiliar: %v\ndirections: %v\nprofile: %s\n", summary.YearsOfExperience, summary.Degree, summary.Field, summary.Expert, summary.Proficient, summary.Familiar, directionLabels(summary.Directions), filepath.Clean(profilePath))
			return err
		}},
	)
	return cmd
}

func directionLabels(directions []profile.Direction) []string {
	labels := make([]string, 0, len(directions))
	for _, direction := range directions {
		labels = append(labels, direction.Key+":"+direction.Title)
	}
	return labels
}
