package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gollyhatch/shelf/internal/epub"
)

func runStats(args ...string) (stdout, stderr string, err error) {
	cmd := statsCmd(".")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestStats(t *testing.T) {
	dir := t.TempDir()
	first := makeEPUB(t, dir)
	second := filepath.Join(dir, "second.epub")
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := epub.Update(first, func(p *epub.Package) error {
		return p.Set(epub.FieldRead, "yes")
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runStats(dir)
	if err != nil {
		t.Fatalf("stats: %v (stderr: %s)", err, stderr)
	}
	for _, want := range []string{
		"Books:        2 (2 readable, 0 unreadable)",
		"Read:         1 · Unread: 1",
		"Authors:      1",
		"Languages:    1",
		"Library size:",
		"Top authors:",
		"Le Guin, Ursula K.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stats output is missing %q:\n%s", want, stdout)
		}
	}

	stdout, stderr, err = runStats("--json", dir)
	if err != nil {
		t.Fatalf("stats --json: %v (stderr: %s)", err, stderr)
	}
	var report statsReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stats JSON: %v\n%s", err, stdout)
	}
	if report.Books != 2 || report.Readable != 2 || report.Unreadable != 0 {
		t.Errorf("book counts = %+v", report)
	}
	if report.Read != 1 || report.Unread != 1 {
		t.Errorf("read counts = %+v", report)
	}
	if report.Authors != 1 || report.Series != 0 || report.Tags != 0 || report.Languages != 1 {
		t.Errorf("metadata counts = %+v", report)
	}
	if report.Size <= 0 {
		t.Errorf("size = %d, want positive", report.Size)
	}
	if len(report.TopAuthors) != 1 || report.TopAuthors[0].Count != 2 {
		t.Errorf("top authors = %+v", report.TopAuthors)
	}
}
