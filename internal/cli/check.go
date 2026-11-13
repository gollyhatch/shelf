package cli

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/gollyhatch/shelf/internal/check"
	"github.com/gollyhatch/shelf/internal/library"
)

func checkCmd(defaultDir string) *cobra.Command {
	var asJSON, recursive bool
	var dir string
	cmd := &cobra.Command{
		Use:     "check [FILE|DIR...]",
		Aliases: []string{"doctor"},
		Short:   "Check EPUB metadata and archive structure",
		Long: `Check reports metadata and archive problems for a shelf.

It looks for unreadable books, missing titles or authors, invalid languages,
dates and episode numbers, series without an episode number or vice versa,
duplicate ISBNs, and suspicious archive structure such as a missing mimetype,
container or package document. With no arguments, the configured library (or
the current directory) is used.

The command exits with a non-zero status when it finds any issue.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := library.Expand(targets(cmd, dir, args, defaultDir), recursive)
			if err != nil {
				return err
			}
			entries := library.Load(paths)
			issues := check.Entries(entries)
			sort.SliceStable(issues, func(i, j int) bool {
				if issues[i].Path != issues[j].Path {
					return issues[i].Path < issues[j].Path
				}
				return issues[i].Message < issues[j].Message
			})
			if issues == nil {
				issues = []check.Issue{}
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(issues); err != nil {
					return err
				}
			} else {
				for _, issue := range issues {
					fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", rel(issue.Path), issue.Message)
				}
			}
			if len(issues) > 0 {
				return &checkIssuesError{count: len(issues)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output machine readable JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recurse into directories")
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	return cmd
}

// checkIssuesError signals that validation completed and found problems.
type checkIssuesError struct {
	count int
}

func (e *checkIssuesError) Error() string {
	if e.count == 1 {
		return "1 issue found"
	}
	return fmt.Sprintf("%d issues found", e.count)
}
