package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) searchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search GitHub Docs",
		Long: `Search GitHub Docs for articles matching the query.

Results include the article title, breadcrumb navigation path, URL,
and a short excerpt from the matching content.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n := a.effectiveLimit(10)
			a.progressf("searching GitHub Docs for %q...", args[0])
			articles, err := a.client.Search(cmd.Context(), args[0], n)
			if err != nil {
				return mapFetchErr(err)
			}
			return a.renderOrEmpty(articles, len(articles))
		},
	}
	return cmd
}
