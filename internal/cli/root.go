// Package cli implements the command line interface: a batch metadata editor
// for EPUB files plus the entry point into the TUI.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gollyhatch/shelf/internal/config"
	"github.com/gollyhatch/shelf/internal/tui"
)

// Execute runs the root command.
func Execute() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := tui.ApplyTheme(cfg.Theme); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := rootCmd(cfg.LibraryDir("."), cfg.Library, cfg.Viewer, cfg.GoogleBooksAPIKey, cfg.Sort, cfg.SearchURL, cfg.RenameFormat, cfg.SlugifyRename()).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const long = `shelf manages the metadata of EPUB files from the terminal.

Run it with no arguments to browse the configured library (or the current
directory when no library is configured), or point it at a shelf of books. The
browser shows the title and author of every EPUB it finds and lets you edit
each Dublin Core field in place. The library is read from
$XDG_CONFIG_HOME/shelf/config.toml (~/.config/shelf/config.toml by default).

Batch edits are available as subcommands and only change the fields you name;
everything else in the package document, including Calibre series information
and cover metadata, is preserved.

  shelf                          browse the library
  shelf ~/books                  browse a shelf
  shelf edit dune.epub           edit a single book
  shelf list --json ~/books      inspect a shelf
  shelf set -a 'Frank Herbert' dune.epub
  shelf set --series Dune --series-index 1 dune.epub
  shelf modify --title -e 'Dune' --replacement 'DUNE' dune.epub
  shelf rename --dry-run -r ~/books
  shelf rename -f '%a - %t.epub' ~/books
  shelf repair -n -r ~/books
  shelf stats ~/books
  shelf check -r ~/books
  shelf completion bash`

func rootCmd(defaultDir, libraryPath, viewer, googleBooksAPIKey, sortSpec, searchURL, renameFormat string, renameSlugify bool) *cobra.Command {
	var dir string
	renameName := renameNameFor(renameFormat, renameSlugify)
	moveName := moveNameFor(renameFormat, renameSlugify)
	cmd := &cobra.Command{
		Use:           "shelf [DIR|FILE]",
		Short:         "Browse and edit EPUB metadata, in your terminal",
		Long:          long,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				dir = args[0]
			}
			if dir == "" {
				dir = defaultDir
			}
			switch {
			case isEPUBFile(dir):
				return tui.RunFile(dir, viewer, googleBooksAPIKey, libraryPath, sortSpec, searchURL, renameName, moveName)
			case !isDir(dir):
				return fmt.Errorf("%s is neither a directory nor an EPUB file", dir)
			}
			return tui.Run(dir, viewer, googleBooksAPIKey, libraryPath, sortSpec, searchURL, renameName, moveName)
		},
	}
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory of EPUB files (overrides the library)")
	cmd.AddCommand(
		listCmd(defaultDir),
		getCmd(defaultDir),
		setCmd(defaultDir),
		modifyCmd(defaultDir),
		editCmd(viewer, googleBooksAPIKey, libraryPath, sortSpec, searchURL, renameName, moveName),
		renameCmd(defaultDir, renameFormat, renameSlugify),
		repairCmd(defaultDir),
		statsCmd(defaultDir),
		checkCmd(defaultDir),
		completionCmd(cmd),
		manCmd(cmd),
	)
	return cmd
}

// withDir appends the value of a -C/--dir flag to the positional arguments so
// that every batch command can be pointed at a shelf instead of listing files.
func withDir(cmd *cobra.Command, dir string, args []string) []string {
	if cmd.Flags().Changed("dir") && strings.TrimSpace(dir) != "" {
		return append(args, dir)
	}
	return args
}

// targets resolves the files or directories a command should operate on. An
// explicit path or -C wins; otherwise the configured library is used, with the
// current directory as the last fallback.
func targets(cmd *cobra.Command, dir string, args []string, defaultDir string) []string {
	args = withDir(cmd, dir, args)
	if len(args) > 0 {
		return args
	}
	if defaultDir == "" {
		defaultDir = "."
	}
	return []string{defaultDir}
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func isEPUBFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && strings.EqualFold(filepath.Ext(path), ".epub")
}

// rel renders a path relative to the working directory when that is shorter.
func rel(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	if r, err := filepath.Rel(wd, path); err == nil && len(r) < len(path) {
		return r
	}
	return path
}
