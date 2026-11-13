package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newTestRoot() *cobra.Command {
	return rootCmd(".", "", "", "", "", "", "", false)
}

func TestCompletionScripts(t *testing.T) {
	root := newTestRoot()
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		cmd := completionCmd(root)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{shell})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("completion %s: %v", shell, err)
		}
		if strings.TrimSpace(out.String()) == "" {
			t.Errorf("completion %s produced no output", shell)
		}
		if !strings.Contains(strings.ToLower(out.String()), "shelf") {
			t.Errorf("completion %s does not mention shelf", shell)
		}
	}
}

func TestManPage(t *testing.T) {
	root := newTestRoot()

	cmd := manCmd(root)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ".TH") || !strings.Contains(out.String(), "SHELF") {
		t.Errorf("man output does not look like a man page:\n%s", out.String())
	}

	dir := t.TempDir()
	cmd = manCmd(root)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shelf.1")); err != nil {
		t.Errorf("man tree did not write shelf.1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shelf-check.1")); err != nil {
		t.Errorf("man tree did not write shelf-check.1: %v", err)
	}
}
