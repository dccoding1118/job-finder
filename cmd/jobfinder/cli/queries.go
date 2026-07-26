package cli

import (
	"fmt"

	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/spf13/cobra"
)

// newQueriesCmd prints what the Profile directions expand into. `show` is for
// checking the expansion while tuning; `urls` is the patrol entry of the
// semi-passive sources: the user opens the printed links themselves and the
// extension harvests the pages they browse. Neither subcommand sends a request.
func newQueriesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "queries", Short: "Show the search queries and patrol URLs derived from Profile"}
	cmd.AddCommand(newQueriesShowCmd(), newQueriesURLsCmd())
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

func newQueriesURLsCmd() *cobra.Command {
	var path, source string
	var pages int
	cmd := &cobra.Command{Use: "urls", Short: "Print the patrol search URLs of a semi-passive source", RunE: func(cmd *cobra.Command, _ []string) error {
		spec, err := querySpec(path)
		if err != nil {
			return err
		}
		pageList, err := crawler.PatrolURLs(source, spec, pages)
		if err != nil {
			return err
		}
		for _, page := range pageList {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\tp%d\t%s\n", page.Direction, page.Page, page.URL); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().StringVar(&path, "config", "config.yaml", "path to config.yaml")
	cmd.Flags().StringVar(&source, "source", "104", "semi-passive source: 104 or cake")
	cmd.Flags().IntVar(&pages, "pages", 2, "result pages to offer per direction")
	return cmd
}

// querySpec reads the same Profile-derived spec the automated fetch uses, so the
// patrol URLs of a semi-passive source cover the same directions.
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
		Area:     snapshot.Profile.Preferences.Locations,
		MaxPages: rt.cfg.Sources.Yourator.MaxPages,
	}, nil
}
