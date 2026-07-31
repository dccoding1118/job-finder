package cli

import (
	"fmt"

	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/spf13/cobra"
)

// newQueriesCmd prints what the Profile directions expand into, for checking the
// expansion while tuning. It sends no request: on the semi-passive sources the
// user searches in their own browser and the extension harvests what they browse.
func newQueriesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "queries", Short: "Show the search queries derived from Profile"}
	cmd.AddCommand(newQueriesShowCmd())
	return cmd
}

func newQueriesShowCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: "show", Short: "Print the expanded search queries of each direction", RunE: func(cmd *cobra.Command, _ []string) error {
		spec, err := querySpec(path)
		if err != nil {
			return err
		}
		for _, query := range spec.Queries {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%v\n", query.Direction, query.Keywords); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().StringVar(&path, "config", "config.yaml", "path to config.yaml")
	return cmd
}

// querySpec reads the same Profile-derived spec the automated fetch uses, so what
// is printed is what a fetch would actually search for.
func querySpec(path string) (crawler.SearchSpec, error) {
	rt, err := loadRuntime(path)
	if err != nil {
		return crawler.SearchSpec{}, err
	}
	defer rt.close()
	snapshot, err := rt.provider.Ready()
	if err != nil {
		return crawler.SearchSpec{}, err
	}
	return crawler.SearchSpec{
		Queries:  directionQueries(*snapshot.Profile),
		MaxPages: rt.cfg.Sources.Yourator.MaxPages,
	}, nil
}
