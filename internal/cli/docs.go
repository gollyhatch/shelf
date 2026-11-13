package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// completionCmd prints a completion script for the requested shell.
func completionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Print a shell completion script",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(out)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(out)
			}
			return fmt.Errorf("unsupported shell %q (want bash, zsh, fish or powershell)", args[0])
		},
	}
}

// manCmd writes a man page for the root command, or a complete tree of pages
// when --dir is set.
func manCmd(root *cobra.Command) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "man",
		Short: "Print or generate man pages",
		Long: `Man prints the root command's roff man page to stdout.

With --dir, it writes a page for every command into that directory.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			header := &doc.GenManHeader{
				Title:   "SHELF",
				Section: "1",
				Source:  "shelf",
				Manual:  "shelf manual",
			}
			if dir == "" {
				return doc.GenMan(root, header, cmd.OutOrStdout())
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			return doc.GenManTree(root, header, dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "write all man pages into this directory")
	return cmd
}
