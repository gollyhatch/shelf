package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
)

// modifyFlags selects the metadata fields a modify run should touch.
type modifyFlags struct {
	selected map[epub.Field]*bool
}

func newModifyFlags() *modifyFlags {
	f := &modifyFlags{selected: make(map[epub.Field]*bool, len(epub.AllFields))}
	for _, field := range epub.AllFields {
		f.selected[field] = new(bool)
	}
	return f
}

func (f *modifyFlags) register(cmd *cobra.Command) {
	for _, field := range epub.AllFields {
		cmd.Flags().BoolVar(f.selected[field], flagName(field), false, modifyFlagHelp(field))
	}
}

func (f *modifyFlags) fields() []epub.Field {
	var out []epub.Field
	for _, field := range epub.AllFields {
		if *f.selected[field] {
			out = append(out, field)
		}
	}
	return out
}

func modifyFlagHelp(field epub.Field) string {
	switch field {
	case epub.FieldAuthor:
		return "modify the author(s)"
	case epub.FieldTags:
		return "modify the tags"
	case epub.FieldSeriesIndex:
		return "modify the episode number"
	}
	return "modify the " + strings.ToLower(field.Label())
}

func modifyCmd(defaultDir string) *cobra.Command {
	flags := newModifyFlags()
	var regexSource, replacement string
	var dryRun, recursive bool
	var dir string
	cmd := &cobra.Command{
		Use:   "modify [flags] [FILE|DIR...]",
		Short: "Modify metadata values with a regular expression",
		Long: `Modify applies a Go regular expression to the existing values of selected
metadata fields. Fields are selected with the same flag names as set, but those
flags are switches here:

  shelf modify --title -e 'Darkness' --replacement 'Night' book.epub
  shelf modify --tags -e '^sf$' --replacement 'Science Fiction' -r ~/books
  shelf modify --author -e '\bLe Guin\b' --replacement 'LeGuin' book.epub

Author and tag values are modified individually. Values that do not match are
left alone, and an empty replacement deletes the matched text. The replacement
supports Go's $1, ${name} expansion. With no arguments, the configured library
(or the current directory) is used.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fields := flags.fields()
			if len(fields) == 0 {
				return fmt.Errorf("nothing to do: select at least one metadata field (see --help)")
			}
			if !cmd.Flags().Changed("regex") {
				return fmt.Errorf("pass --regex/-e with the regular expression to apply")
			}
			if regexSource == "" {
				return fmt.Errorf("--regex must not be empty")
			}
			re, err := regexp.Compile(regexSource)
			if err != nil {
				return fmt.Errorf("invalid --regex: %w", err)
			}

			files := targets(cmd, dir, args, defaultDir)
			paths, err := library.Expand(files, recursive)
			if err != nil {
				return err
			}
			apply := func(p *epub.Package) error {
				return modifyPackage(p, fields, re, replacement)
			}
			previewFn := func(m epub.Metadata) epub.Metadata {
				return modifyMetadata(m, fields, re, replacement)
			}
			return applyFiles(paths, dryRun, "modified", apply, previewFn)
		},
	}
	flags.register(cmd)
	cmd.Flags().StringVarP(&regexSource, "regex", "e", "", "Go regular expression to apply")
	cmd.Flags().StringVar(&replacement, "replacement", "", "replacement text (supports $1, ${name})")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would change without writing")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "recurse into directories")
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	return cmd
}

// modifyPackage applies the expression to the selected fields of an open EPUB.
func modifyPackage(p *epub.Package, fields []epub.Field, re *regexp.Regexp, replacement string) error {
	meta := p.Metadata()
	for _, field := range fields {
		switch field {
		case epub.FieldAuthor:
			current := meta.Creators
			updated := make([]epub.Creator, 0, len(current))
			for _, creator := range current {
				name := strings.TrimSpace(re.ReplaceAllString(creator.Name, replacement))
				if name != "" {
					updated = append(updated, epub.Creator{Name: name})
				}
			}
			if !slices.Equal(current, updated) {
				if err := p.SetCreators(updated); err != nil {
					return err
				}
			}

		case epub.FieldTags:
			current := meta.Subjects
			updated := make([]string, 0, len(current))
			for _, subject := range current {
				value := strings.TrimSpace(re.ReplaceAllString(subject, replacement))
				if value != "" {
					updated = append(updated, value)
				}
			}
			if !slices.Equal(current, updated) {
				if err := p.SetSubjects(updated); err != nil {
					return err
				}
			}

		default:
			current := strings.TrimSpace(meta.Get(field))
			if current == "" {
				continue
			}
			updated := strings.TrimSpace(re.ReplaceAllString(current, replacement))
			if updated != current {
				if err := p.Set(field, updated); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// modifyMetadata is the dry-run equivalent of modifyPackage.
func modifyMetadata(m epub.Metadata, fields []epub.Field, re *regexp.Regexp, replacement string) epub.Metadata {
	for _, field := range fields {
		switch field {
		case epub.FieldAuthor:
			updated := make([]epub.Creator, 0, len(m.Creators))
			for _, creator := range m.Creators {
				name := strings.TrimSpace(re.ReplaceAllString(creator.Name, replacement))
				if name != "" {
					updated = append(updated, epub.Creator{Name: name})
				}
			}
			m.Creators = updated

		case epub.FieldTags:
			updated := make([]string, 0, len(m.Subjects))
			for _, subject := range m.Subjects {
				value := strings.TrimSpace(re.ReplaceAllString(subject, replacement))
				if value != "" {
					updated = append(updated, value)
				}
			}
			m.Subjects = updated

		default:
			current := strings.TrimSpace(m.Get(field))
			if current == "" {
				continue
			}
			setMetadataField(&m, field, strings.TrimSpace(re.ReplaceAllString(current, replacement)))
		}
	}
	return m
}

// setMetadataField stores a scalar field on an in-memory Metadata value.
func setMetadataField(m *epub.Metadata, field epub.Field, value string) {
	switch field {
	case epub.FieldTitle:
		m.Title = value
	case epub.FieldSubtitle:
		m.Subtitle = value
	case epub.FieldAuthor:
		m.Creators = epub.ParseCreatorList(value)
	case epub.FieldTags:
		m.Subjects = epub.SplitList(value, ",")
	case epub.FieldSeries:
		m.Series = value
	case epub.FieldSeriesIndex:
		m.SeriesIndex = value
	case epub.FieldDescription:
		m.Description = value
	case epub.FieldPublisher:
		m.Publisher = value
	case epub.FieldDate:
		m.Date = value
	case epub.FieldLanguage:
		m.Language = value
	case epub.FieldRights:
		m.Rights = value
	case epub.FieldIdentifier:
		m.Identifier = value
	case epub.FieldRead:
		m.Read = epub.ParseReadStatus(value)
	}
}
