package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gollyhatch/shelf/internal/library"
)

// statCount is one named count in the human and JSON reports.
type statCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// statsReport is the stable JSON shape of the stats command.
type statsReport struct {
	Books       int            `json:"books"`
	Readable    int            `json:"readable"`
	Unreadable  int            `json:"unreadable"`
	Read        int            `json:"read"`
	Unread      int            `json:"unread"`
	Authors     int            `json:"authors"`
	Series      int            `json:"series"`
	Tags        int            `json:"tags"`
	Languages   int            `json:"languages"`
	Size        int64          `json:"size"`
	LanguagesBy map[string]int `json:"languagesByName,omitempty"`
	TopAuthors  []statCount    `json:"topAuthors,omitempty"`
	TopSeries   []statCount    `json:"topSeries,omitempty"`
}

func statsCmd(defaultDir string) *cobra.Command {
	var asJSON, recursive bool
	var top int
	var dir string
	cmd := &cobra.Command{
		Use:   "stats [FILE|DIR...]",
		Short: "Show library statistics",
		Long: `Stats counts the books on a shelf and summarises their metadata.

It reports readable and unreadable files, read and unread books, unique
authors, series, tags and languages, the total size on disk, and the most
common authors and series. With no arguments, the configured library (or the
current directory) is used.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := library.Expand(targets(cmd, dir, args, defaultDir), recursive)
			if err != nil {
				return err
			}
			entries := library.Load(paths)
			report, err := buildStats(entries, top)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			writeStats(cmd.OutOrStdout(), report)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output machine readable JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recurse into directories")
	cmd.Flags().IntVar(&top, "top", 10, "number of authors and series to list")
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	return cmd
}

func buildStats(entries []library.Entry, top int) (statsReport, error) {
	report := statsReport{LanguagesBy: make(map[string]int)}
	if top < 0 {
		top = 0
	}

	authors := make(map[string]*statCount)
	series := make(map[string]*statCount)
	tags := make(map[string]struct{})
	languages := make(map[string]struct{})

	for _, e := range entries {
		report.Books++
		if e.Err != nil {
			report.Unreadable++
		} else {
			report.Readable++
		}
		if e.Metadata.Read {
			report.Read++
		} else {
			report.Unread++
		}

		if st, err := os.Stat(e.Path); err == nil {
			report.Size += st.Size()
		}

		for _, name := range e.Metadata.CreatorNames() {
			addStat(authors, name)
		}
		if name := strings.TrimSpace(e.Metadata.Series); name != "" {
			addStat(series, name)
		}
		for _, subject := range e.Metadata.Subjects {
			if subject = strings.TrimSpace(subject); subject != "" {
				tags[strings.ToLower(subject)] = struct{}{}
			}
		}
		if language := strings.TrimSpace(e.Metadata.Language); language != "" {
			report.LanguagesBy[language]++
			languages[strings.ToLower(language)] = struct{}{}
		}
	}

	report.Authors = len(authors)
	report.Series = len(series)
	report.Tags = len(tags)
	report.Languages = len(languages)
	report.TopAuthors = topStats(authors, top)
	report.TopSeries = topStats(series, top)
	return report, nil
}

// addStat increments a case-insensitive named counter while keeping the first
// spelling that was seen.
func addStat(counts map[string]*statCount, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	key := strings.ToLower(name)
	if item, ok := counts[key]; ok {
		item.Count++
		return
	}
	counts[key] = &statCount{Name: name, Count: 1}
}

func topStats(counts map[string]*statCount, limit int) []statCount {
	out := make([]statCount, 0, len(counts))
	for _, item := range counts {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

func writeStats(w io.Writer, report statsReport) {
	fmt.Fprintf(w, "Books:        %d (%d readable, %d unreadable)\n", report.Books, report.Readable, report.Unreadable)
	fmt.Fprintf(w, "Read:         %d · Unread: %d\n", report.Read, report.Unread)
	fmt.Fprintf(w, "Authors:      %d\n", report.Authors)
	fmt.Fprintf(w, "Series:       %d\n", report.Series)
	fmt.Fprintf(w, "Tags:         %d\n", report.Tags)
	fmt.Fprintf(w, "Languages:    %d\n", report.Languages)
	fmt.Fprintf(w, "Library size: %s\n", humanBytes(report.Size))

	if len(report.LanguagesBy) > 0 {
		fmt.Fprintln(w, "\nLanguages:")
		keys := make([]string, 0, len(report.LanguagesBy))
		for language := range report.LanguagesBy {
			keys = append(keys, language)
		}
		sort.Slice(keys, func(i, j int) bool {
			if report.LanguagesBy[keys[i]] != report.LanguagesBy[keys[j]] {
				return report.LanguagesBy[keys[i]] > report.LanguagesBy[keys[j]]
			}
			return strings.ToLower(keys[i]) < strings.ToLower(keys[j])
		})
		for _, language := range keys {
			fmt.Fprintf(w, "  %-12s %d\n", language, report.LanguagesBy[language])
		}
	}
	writeTopStats(w, "Top authors", report.TopAuthors)
	writeTopStats(w, "Top series", report.TopSeries)
}

func writeTopStats(w io.Writer, title string, counts []statCount) {
	if len(counts) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, item := range counts {
		fmt.Fprintf(w, "  %4d  %s\n", item.Count, item.Name)
	}
}

func humanBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}
