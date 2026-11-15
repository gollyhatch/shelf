package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
	"github.com/gollyhatch/shelf/internal/tui"
)

// ---------- list ----------

func listCmd(defaultDir string) *cobra.Command {
	var asJSON bool
	var recursive bool
	var dir string
	cmd := &cobra.Command{
		Use:   "list [FILE|DIR...]",
		Short: "List EPUB files with their metadata",
		Long: `List prints the metadata of one or more EPUBs.

Arguments may be EPUB files or directories; with --recursive, directories are
searched for .epub files at any depth. With no arguments, the configured
library (or the current directory) is listed.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := library.Expand(targets(cmd, dir, args, defaultDir), recursive)
			if err != nil {
				return err
			}
			entries := library.Load(paths)
			if asJSON {
				return writeJSON(os.Stdout, entries)
			}
			var failed bool
			for i, e := range entries {
				if e.Err != nil {
					failed = true
					fmt.Fprintf(os.Stderr, "%s: %v\n", rel(e.Path), e.Err)
					continue
				}
				if i > 0 {
					fmt.Fprintln(os.Stdout)
				}
				writeEntry(os.Stdout, e)
			}
			if failed {
				os.Exit(2)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output machine readable JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recurse into directories")
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	return cmd
}

func writeEntry(w io.Writer, e library.Entry) {
	m := e.Metadata
	fmt.Fprintln(w, rel(e.Path))
	row := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		fmt.Fprintf(w, "  %-13s %s\n", label+":", firstLine(value))
	}
	row("Title", m.Title)
	row("Subtitle", m.Subtitle)
	row("Author", m.AuthorString())
	row("Tags", strings.Join(m.Subjects, ", "))
	row("Series", m.Series)
	row("Episode", m.SeriesIndex)
	row("Publisher", m.Publisher)
	row("Date", m.Date)
	row("Language", m.Language)
	row("Rights", m.Rights)
	row("Identifier", m.Identifier)
	row("Description", m.Description)
	if m.Read {
		row("Read", "yes")
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimRight(s[:i], " \t") + " …"
	}
	return s
}

// jsonEntry is the stable JSON shape emitted by list --json.
type jsonEntry struct {
	Path        string   `json:"path"`
	Title       string   `json:"title,omitempty"`
	Subtitle    string   `json:"subtitle,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Series      string   `json:"series,omitempty"`
	SeriesIndex string   `json:"seriesIndex,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Date        string   `json:"date,omitempty"`
	Language    string   `json:"language,omitempty"`
	Rights      string   `json:"rights,omitempty"`
	Identifier  string   `json:"identifier,omitempty"`
	Description string   `json:"description,omitempty"`
	Read        bool     `json:"read,omitempty"`
}

func writeJSON(w io.Writer, entries []library.Entry) error {
	out := make([]jsonEntry, 0, len(entries))
	for _, e := range entries {
		m := e.Metadata
		out = append(out, jsonEntry{
			Path:        e.Path,
			Title:       m.Title,
			Subtitle:    m.Subtitle,
			Authors:     m.CreatorNames(),
			Tags:        m.Subjects,
			Series:      m.Series,
			SeriesIndex: m.SeriesIndex,
			Publisher:   m.Publisher,
			Date:        m.Date,
			Language:    m.Language,
			Rights:      m.Rights,
			Identifier:  m.Identifier,
			Description: m.Description,
			Read:        m.Read,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ---------- get ----------

func getCmd(defaultDir string) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "get FIELD [FILE|DIR...]",
		Short: "Print a single metadata field, for use in scripts",
		Long: `Get prints one metadata field of one or more EPUB files.

Field names are case insensitive and accept common aliases, e.g. author,
creator, tags, subject, series, series-index, episode, isbn, publisher,
language, description. With no files, the configured library (or the current
directory) is used.

  shelf get title book.epub
  shelf get author *.epub
  shelf get title`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("usage: shelf get FIELD [FILE|DIR...] (or -C DIR)")
			}
			field, ok := epub.ParseField(args[0])
			if !ok {
				return fmt.Errorf("unknown field %q (try: %s)", args[0], fieldNames())
			}
			files := targets(cmd, dir, args[1:], defaultDir)
			paths, err := library.Expand(files, false)
			if err != nil {
				return err
			}
			for _, p := range paths {
				e := library.LoadOne(p)
				if e.Err != nil {
					fmt.Fprintf(os.Stderr, "%s: %v\n", rel(p), e.Err)
					continue
				}
				value := e.Metadata.Get(field)
				if len(paths) > 1 {
					fmt.Printf("%s\t%s\n", rel(p), value)
					continue
				}
				fmt.Println(value)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	return cmd
}

func fieldNames() string {
	names := make([]string, 0, len(epub.AllFields))
	for _, f := range epub.AllFields {
		names = append(names, string(f))
	}
	return strings.Join(names, ", ")
}

// ---------- set / edit ----------

// changeFlags is the shared metadata flag set of the batch commands.
type changeFlags struct {
	values    map[epub.Field]*[]string
	append    bool
	dryRun    bool
	recursive bool
}

func newChangeFlags() *changeFlags {
	f := &changeFlags{values: map[epub.Field]*[]string{}}
	for _, field := range []epub.Field{
		epub.FieldTitle, epub.FieldSubtitle, epub.FieldAuthor, epub.FieldTags,
		epub.FieldSeries, epub.FieldSeriesIndex,
		epub.FieldDescription, epub.FieldPublisher, epub.FieldDate,
		epub.FieldLanguage, epub.FieldRights, epub.FieldIdentifier, epub.FieldRead,
	} {
		f.values[field] = new([]string)
	}
	return f
}

func (f *changeFlags) register(cmd *cobra.Command) {
	pf := cmd.Flags()
	pf.StringArrayVar(f.values[epub.FieldTitle], "title", nil, "set the title")
	pf.StringArrayVar(f.values[epub.FieldSubtitle], "subtitle", nil, "set the subtitle")
	pf.StringArrayVarP(f.values[epub.FieldAuthor], "author", "a", nil, "set the author(s), separate several with ';'")
	pf.StringArrayVar(f.values[epub.FieldTags], "tags", nil, "set the tags/subjects, comma separated")
	pf.StringArrayVar(f.values[epub.FieldSeries], "series", nil, "set the series name")
	pf.StringArrayVar(f.values[epub.FieldSeriesIndex], "series-index", nil, "set the book's number within its series")
	pf.StringArrayVarP(f.values[epub.FieldDescription], "description", "d", nil, "set the description")
	pf.StringArrayVar(f.values[epub.FieldPublisher], "publisher", nil, "set the publisher")
	pf.StringArrayVar(f.values[epub.FieldDate], "date", nil, "set the publication date")
	pf.StringArrayVarP(f.values[epub.FieldLanguage], "language", "l", nil, "set the language, e.g. en")
	pf.StringArrayVar(f.values[epub.FieldRights], "rights", nil, "set the rights/copyright statement")
	pf.StringArrayVar(f.values[epub.FieldIdentifier], "isbn", nil, "set the identifier/ISBN")
	pf.StringArrayVar(f.values[epub.FieldRead], "read", nil, "mark the book as read (yes/no)")
	pf.BoolVar(&f.append, "append", false, "append to multi-valued fields instead of replacing them")
	pf.BoolVarP(&f.dryRun, "dry-run", "n", false, "show what would change without writing")
	pf.BoolVarP(&f.recursive, "recursive", "r", false, "recurse into directories")
}

// changes returns the requested field updates in a deterministic order.
func (f *changeFlags) changes(cmd *cobra.Command) []epub.Field {
	var out []epub.Field
	for _, field := range epub.AllFields {
		if v := f.values[field]; v != nil && len(*v) > 0 && cmd.Flags().Changed(flagName(field)) {
			out = append(out, field)
		}
	}
	return out
}

// flagName returns the command line flag that carries a field's value. The
// identifier is exposed as --isbn to match its most common meaning.
func flagName(field epub.Field) string {
	if field == epub.FieldIdentifier {
		return "isbn"
	}
	return string(field)
}

// value collapses repeated flags: multi-valued fields are joined, singular
// fields take the last value given.
func (f *changeFlags) value(field epub.Field) string {
	v := *f.values[field]
	switch field {
	case epub.FieldAuthor:
		return strings.Join(v, "; ")
	case epub.FieldTags:
		return strings.Join(v, ",")
	default:
		return v[len(v)-1]
	}
}

func setCmd(defaultDir string) *cobra.Command {
	flags := newChangeFlags()
	var dir string
	cmd := &cobra.Command{
		Use:   "set [flags] [FILE|DIR...]",
		Short: "Set metadata fields on one or many EPUB files",
		Long: `Set rewrites the requested metadata fields of every named EPUB.

Arguments may be EPUB files or directories. With no arguments, the configured
library (or the current directory) is used. Only the fields you pass are
changed: everything else in the package document, including Calibre series
information and cover references, is preserved.

  shelf set -a 'Frank Herbert' dune.epub
  shelf set --tags 'scifi, classic' -l en *.epub
  shelf set --series 'Dune' --series-index 1 dune.epub
  shelf set -a 'K. Le Guin' --append fantasy/*.epub
  shelf set --title '' book.epub        # remove the title element

Use --dry-run to preview and --append for multi-valued fields.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fields := flags.changes(cmd)
			if len(fields) == 0 {
				return fmt.Errorf("nothing to do: pass at least one metadata flag (see --help)")
			}
			files := targets(cmd, dir, args, defaultDir)
			paths, err := library.Expand(files, flags.recursive)
			if err != nil {
				return err
			}
			return applyChanges(paths, flags, fields)
		},
	}
	flags.register(cmd)
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	return cmd
}

func applyChanges(paths []string, flags *changeFlags, fields []epub.Field) error {
	updates := make(map[epub.Field]string, len(fields))
	for _, field := range fields {
		updates[field] = flags.value(field)
	}
	apply := func(p *epub.Package) error {
		for _, field := range fields {
			if err := p.SetWith(field, updates[field], flags.append); err != nil {
				return err
			}
		}
		return nil
	}
	previewFn := func(m epub.Metadata) epub.Metadata {
		return preview(m, updates, fields, flags.append)
	}
	return applyFiles(paths, flags.dryRun, "updated", apply, previewFn)
}

// applyFiles applies one transformation to every file, reports the metadata
// diff, and continues past per-file failures.
func applyFiles(
	paths []string,
	dryRun bool,
	action string,
	apply func(*epub.Package) error,
	previewFn func(epub.Metadata) epub.Metadata,
) error {
	var failed bool
	for _, path := range paths {
		before := library.LoadOne(path)
		if before.Err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "%s: %v\n", rel(path), before.Err)
			continue
		}
		after := before
		if !dryRun {
			if err := epub.Update(path, apply); err != nil {
				failed = true
				fmt.Fprintf(os.Stderr, "%s: %v\n", rel(path), err)
				continue
			}
			after = library.LoadOne(path)
			if after.Err != nil {
				after = before
			}
		} else {
			after.Metadata = previewFn(before.Metadata)
		}
		report(os.Stdout, rel(path), before.Metadata, after.Metadata, dryRun)
	}
	if failed {
		return fmt.Errorf("some files could not be %s", action)
	}
	return nil
}

// preview applies changes to an in-memory Metadata for --dry-run reporting.
func preview(m epub.Metadata, updates map[epub.Field]string, fields []epub.Field, appendMode bool) epub.Metadata {
	join := func(current, value string) string {
		if appendMode && current != "" && value != "" {
			return current + ", " + value
		}
		return value
	}
	for _, field := range fields {
		value := strings.TrimSpace(updates[field])
		switch field {
		case epub.FieldTitle:
			m.Title = join(m.Title, value)
		case epub.FieldSubtitle:
			m.Subtitle = join(m.Subtitle, value)
		case epub.FieldAuthor:
			if appendMode {
				m.Creators = append(m.Creators, epub.ParseCreatorList(value)...)
			} else {
				m.Creators = epub.ParseCreatorList(value)
			}
		case epub.FieldTags:
			if appendMode {
				m.Subjects = append(m.Subjects, epub.SplitList(value, ",")...)
			} else {
				m.Subjects = epub.SplitList(value, ",")
			}
		case epub.FieldSeries:
			m.Series = join(m.Series, value)
		case epub.FieldSeriesIndex:
			m.SeriesIndex = value
		case epub.FieldDescription:
			m.Description = join(m.Description, value)
		case epub.FieldPublisher:
			m.Publisher = join(m.Publisher, value)
		case epub.FieldDate:
			m.Date = join(m.Date, value)
		case epub.FieldLanguage:
			m.Language = join(m.Language, value)
		case epub.FieldRights:
			m.Rights = join(m.Rights, value)
		case epub.FieldIdentifier:
			m.Identifier = join(m.Identifier, value)
		}
	}
	return m
}

func report(w io.Writer, path string, before, after epub.Metadata, dry bool) {
	prefix := ""
	if dry {
		prefix = "would update "
	}
	header := prefix + path
	lines := diffFields(before, after)
	if len(lines) == 0 {
		fmt.Fprintf(w, "%s (unchanged)\n", path)
		return
	}
	fmt.Fprintln(w, header)
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

func diffFields(before, after epub.Metadata) []string {
	var out []string
	for _, field := range epub.AllFields {
		b, a := before.Get(field), after.Get(field)
		if b == a {
			continue
		}
		out = append(out, fmt.Sprintf("%-11s %s", field.Label()+":", change(b, a)))
	}
	return out
}

func change(before, after string) string {
	switch {
	case before == "":
		return "+ " + quote(after)
	case after == "":
		return "- " + quote(before)
	default:
		return quote(before) + " → " + quote(after)
	}
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " ") {
		return `"` + s + `"`
	}
	return s
}

// ---------- edit ----------

func editCmd(viewer, googleBooksAPIKey, libraryPath, sortSpec, searchURL string, renameName, moveName func(library.Entry) (string, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit FILE",
		Short: "Open the interactive editor for a single EPUB",
		Long: `Edit opens the TUI focused on a single file, which is the quickest way to
inspect and fix one book's metadata.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if !strings.EqualFold(filepath.Ext(args[0]), ".epub") {
				return fmt.Errorf("%s does not look like an EPUB file", args[0])
			}
			return tui.RunFile(args[0], viewer, googleBooksAPIKey, libraryPath, sortSpec, searchURL, renameName, moveName)
		},
	}
	return cmd
}

// ---------- rename ----------

func renameCmd(defaultDir, defaultFormat string, defaultSlugify bool) *cobra.Command {
	var dryRun bool
	var recursive bool
	var dir string
	var format = defaultFormat
	var slugifyFlag, noSlugify bool
	cmd := &cobra.Command{
		Use:   "rename [FILE|DIR...]",
		Short: "Rename files to match their title (author - title.epub)",
		Long: `Rename renames each EPUB after the book it contains, so that a shelf of
files downloaded from various sources becomes readable in a file manager.
With no arguments, the configured library (or the current directory) is used.

  shelf rename ~/books/hobbit.epub
  shelf rename --dry-run -r ~/books
  shelf rename -f '%a - %t.epub' ~/books
  shelf rename --slugify -f '%a-%t.epub' ~/books
  shelf rename --no-slugify ~/books

When -f is omitted, the rename_format value from the configuration file is
used if set; otherwise the built-in author - title name is used.

With --format the name comes from a pattern whose placeholders are replaced
with the book's metadata:

  %t title      %b subtitle    %a author     %g tags
  %s series     %e episode     %p publisher  %d date
  %l language   %r rights      %i identifier % a literal percent

Field names work as well (%title, %series-index, %isbn, ...). Episode numbers
ending in .0 are shortened (2.0 -> 2); fractional values are kept (2.5). Each
value is made safe for file names, and .epub is appended when the pattern does
not already end with it.

With --slugify (-s), metadata values are lowercased, spaces become dashes,
German umlauts and ß are expanded (ü -> ue, ß -> ss), accents are folded to
ASCII, and other special characters are converted or removed. Whitespace in
the --format pattern also becomes dashes. The default author - title name uses
a single dash between the two fields. Set slugify = true in the configuration
to make this the default; --no-slugify disables it for one run.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFormat(format); err != nil {
				return err
			}
			slugifyNames := defaultSlugify
			if cmd.Flags().Changed("slugify") || cmd.Flags().Changed("sluggify") {
				slugifyNames = slugifyFlag
			}
			if noSlugify {
				slugifyNames = false
			}
			files := targets(cmd, dir, args, defaultDir)
			paths, err := library.Expand(files, recursive)
			if err != nil {
				return err
			}
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			for _, path := range paths {
				e := library.LoadOne(path)
				if e.Err != nil {
					fmt.Fprintf(errOut, "%s: %v\n", rel(path), e.Err)
					continue
				}
				name := filename(e, slugifyNames)
				if format != "" {
					name = expandFormat(format, e, slugifyNames)
				}
				target := filepath.Join(filepath.Dir(path), name)
				if target == path {
					fmt.Fprintf(out, "%s (already named correctly)\n", rel(path))
					continue
				}
				if dryRun {
					fmt.Fprintf(out, "%s → %s\n", rel(path), filepath.Base(target))
					continue
				}
				if _, err := os.Stat(target); err == nil {
					fmt.Fprintf(errOut, "%s: %s already exists\n", rel(path), filepath.Base(target))
					continue
				}
				if err := os.Rename(path, target); err != nil {
					fmt.Fprintf(errOut, "%s: %v\n", rel(path), err)
					continue
				}
				fmt.Fprintf(out, "%s → %s\n", rel(path), filepath.Base(target))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show the new names without renaming")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recurse into directories")
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	cmd.Flags().StringVarP(&format, "format", "f", defaultFormat, "file name pattern, e.g. '%a - %t.epub'")
	cmd.Flags().BoolVarP(&slugifyFlag, "slugify", "s", false, "force slugified metadata values (overrides the config)")
	cmd.Flags().BoolVar(&slugifyFlag, "sluggify", false, "alias for --slugify")
	_ = cmd.Flags().MarkHidden("sluggify")
	cmd.Flags().BoolVar(&noSlugify, "no-slugify", false, "disable slugify from the config for this run")
	cmd.Flags().BoolVar(&noSlugify, "no-sluggify", false, "alias for --no-slugify")
	_ = cmd.Flags().MarkHidden("no-sluggify")
	return cmd
}

// ---------- repair ----------

func repairCmd(defaultDir string) *cobra.Command {
	var dryRun bool
	var recursive bool
	var dir string
	cmd := &cobra.Command{
		Use:   "repair [FILE|DIR...]",
		Short: "Rewrite EPUB package XML to fix metadata problems",
		Long: `Repair rewrites the package document of each EPUB to restore the conventional
dc: XML namespace prefixes used by most reading systems. It also normalizes
books that contain the same metadata key in both <meta name="..."> and
<meta property="..."> form. Metadata values are not changed, and files that
are already correct are left untouched.

The shapes it fixes were produced by older shelf releases; current saves are
already clean and reading tolerates duplicate keys, so repair is a one-time
migration rather than part of the normal edit flow.

Arguments may be EPUB files or directories. With no arguments, the configured
library (or the current directory) is used. Use --dry-run to preview and
--recursive to include subdirectories.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := library.Expand(targets(cmd, dir, args, defaultDir), recursive)
			if err != nil {
				return err
			}
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			var failed bool
			for _, path := range paths {
				changed, err := epub.Repair(path, dryRun)
				if err != nil {
					failed = true
					fmt.Fprintf(errOut, "%s: %v\n", rel(path), err)
					continue
				}
				switch {
				case !changed:
					fmt.Fprintf(out, "%s (unchanged)\n", rel(path))
				case dryRun:
					fmt.Fprintf(out, "would repair %s\n", rel(path))
				default:
					fmt.Fprintf(out, "%s (repaired)\n", rel(path))
				}
			}
			if failed {
				return fmt.Errorf("some files could not be repaired")
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be rewritten without writing")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recurse into directories")
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	return cmd
}

func filename(e library.Entry, slugifyNames bool) string {
	title := strings.TrimSpace(e.Metadata.Title)
	if title == "" {
		title = strings.TrimSpace(e.Title())
	}
	author := shortAuthor(e.Metadata.AuthorString())

	separator := " - "
	if slugifyNames {
		title = slugify(title)
		author = slugify(author)
		separator = "-"
	} else {
		title = sanitize(title)
		author = sanitize(author)
	}

	switch {
	case author != "" && title != "":
		return author + separator + title + ".epub"
	case title != "":
		return title + ".epub"
	default:
		return filepath.Base(e.Path)
	}
}

// shortAuthor trims an author list down to the first name so that file names
// stay manageable.
func shortAuthor(s string) string {
	names := strings.Split(s, ",")
	return strings.TrimSpace(names[0])
}

// formatVerbs maps the single letter placeholders of --format to metadata
// fields. Longer field names (%title, %series-index, ...) work too.
var formatVerbs = map[byte]epub.Field{
	't': epub.FieldTitle,
	'b': epub.FieldSubtitle,
	'a': epub.FieldAuthor,
	'g': epub.FieldTags,
	's': epub.FieldSeries,
	'e': epub.FieldSeriesIndex,
	'p': epub.FieldPublisher,
	'd': epub.FieldDate,
	'l': epub.FieldLanguage,
	'r': epub.FieldRights,
	'i': epub.FieldIdentifier,
}

// formatVerbList is the shorthand list quoted in error messages.
const formatVerbList = "%t %b %a %g %s %e %p %d %l %r %i, or % for a literal percent"

// validateFormat reports the first problem with a --format pattern so that a
// typo is caught before any file is renamed.
func validateFormat(format string) error {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i >= len(format) {
			return fmt.Errorf("format ends with a lone %%")
		}
		if format[i] == '%' {
			continue
		}
		_, end, ok := formatVerbAt(format, i)
		if !ok {
			j := i
			for j < len(format) && isFormatVerbChar(format[j]) {
				j++
			}
			if j == i {
				return fmt.Errorf("%% must be followed by a placeholder (%s)", formatVerbList)
			}
			return fmt.Errorf("unknown placeholder %%%s in format; valid: %s", format[i:j], formatVerbList)
		}
		i = end - 1
	}
	return nil
}

// lookupFormatVerb resolves one placeholder (the text after a sign) to the
// field it expands to. A single letter is shorthand; longer words are field
// names as accepted by the set and get commands.
func lookupFormatVerb(word string) (epub.Field, bool) {
	if len(word) == 1 {
		f, ok := formatVerbs[word[0]]
		return f, ok
	}
	return epub.ParseField(word)
}

// isFormatVerbChar reports whether c can appear inside a placeholder, so
// that %series-index reads as one placeholder while %a-%t reads as two.
func isFormatVerbChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-'
}

// formatVerbAt resolves the placeholder starting immediately after a percent
// sign. It returns the exclusive end of the placeholder. A trailing dash that
// is not part of a known field name is left for the literal pattern, so
// "%a-%t" works as an author-title separator.
func formatVerbAt(format string, start int) (epub.Field, int, bool) {
	j := start
	for j < len(format) && isFormatVerbChar(format[j]) {
		j++
	}
	if j == start {
		return "", start, false
	}
	if f, ok := lookupFormatVerb(format[start:j]); ok {
		return f, j, true
	}
	if format[j-1] == '-' {
		if f, ok := lookupFormatVerb(format[start : j-1]); ok {
			return f, j - 1, true
		}
	}
	return "", start, false
}

// expandFormat replaces the placeholders of a rename pattern with the book's
// metadata and makes the result safe to use as a file name. It assumes the
// format was accepted by validateFormat; an empty expansion falls back to the
// default name. With slugify set, each metadata value is slugified before it
// is inserted; literal pattern text is left as written.
func expandFormat(format string, e library.Entry, slugifyNames bool) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			b.WriteByte(format[i])
			continue
		}
		i++
		if i >= len(format) {
			break
		}
		if format[i] == '%' {
			b.WriteByte('%')
			continue
		}
		j := i
		for j < len(format) && isFormatVerbChar(format[j]) {
			j++
		}
		if f, end, ok := formatVerbAt(format, i); ok {
			value := e.Metadata.Get(f)
			if f == epub.FieldSeriesIndex {
				value = normalizeEpisode(value)
			}
			if slugifyNames {
				value = slugify(value)
			}
			b.WriteString(value)
			i = end - 1
			continue
		}
		b.WriteString(format[i:j])
		i = j - 1
	}
	// Sanitize once, over the whole expanded name: values and the pattern's
	// literal text alike must not be able to introduce a path separator.
	name := sanitize(b.String())
	if slugifyNames {
		name = dashWhitespace(name)
	}
	if name == "" {
		return filename(e, slugifyNames)
	}
	if !strings.EqualFold(filepath.Ext(name), ".epub") {
		name += ".epub"
	}
	return name
}

// normalizeEpisode drops a trailing .0 from an episode number, so 2.0 becomes
// 2 while 2.5 is left alone.
func normalizeEpisode(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ".0") {
		return strings.TrimSuffix(s, ".0")
	}
	return s
}

// renameNameFor builds the name generator used by the TUI rename action. It
// shares the CLI's formatting and slugify rules.
func renameNameFor(format string, slugifyNames bool) func(library.Entry) (string, error) {
	return func(e library.Entry) (string, error) {
		if format == "" {
			return filename(e, slugifyNames), nil
		}
		if err := validateFormat(format); err != nil {
			return "", err
		}
		return expandFormat(format, e, slugifyNames), nil
	}
}

// moveNameFor builds the name generator for moving a book into the library. It
// returns nil unless rename_format is configured, because this action must not
// fall back to the built-in default name.
func moveNameFor(format string, slugifyNames bool) func(library.Entry) (string, error) {
	if strings.TrimSpace(format) == "" {
		return nil
	}
	return renameNameFor(format, slugifyNames)
}

// sanitize makes a string safe for a file name.
func sanitize(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", " -", "*", "", "?", "",
		"\"", "'", "<", "(", ">", ")", "|", "-", "\n", " ", "\r", " ", "\t", " ",
	)
	s = replacer.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " .-")
	if len(s) > 120 {
		s = strings.TrimSpace(s[:120])
	}
	return s
}
