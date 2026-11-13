package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gollyhatch/shelf/internal/check"
	"github.com/gollyhatch/shelf/internal/epub"
)

func runCheck(args ...string) (stdout, stderr string, err error) {
	cmd := checkCmd(".")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestCheckCleanBook(t *testing.T) {
	dir := t.TempDir()
	makeEPUB(t, dir)

	stdout, stderr, err := runCheck(dir)
	if err != nil {
		t.Fatalf("check: %v (stderr: %s)\n%s", err, stderr, stdout)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("clean book produced issues:\n%s", stdout)
	}
}

func TestCheckFindsMetadataIssues(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)
	if err := epub.Update(path, func(p *epub.Package) error {
		if err := p.Set(epub.FieldTitle, ""); err != nil {
			return err
		}
		if err := p.Set(epub.FieldAuthor, ""); err != nil {
			return err
		}
		if err := p.Set(epub.FieldLanguage, "not a language!"); err != nil {
			return err
		}
		return p.Set(epub.FieldSeries, "Saga")
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCheck(dir)
	if err == nil {
		t.Fatalf("check reported no error for a broken book (stderr: %s)\n%s", stderr, stdout)
	}
	for _, want := range []string{
		"missing title",
		"missing author",
		`invalid language "not a language!"`,
		`series "Saga" has no episode number`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("check output is missing %q:\n%s", want, stdout)
		}
	}
}

func TestCheckDuplicateISBNAndJSON(t *testing.T) {
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
	for _, path := range []string{first, second} {
		if err := epub.Update(path, func(p *epub.Package) error {
			return p.Set(epub.FieldIdentifier, "9780141439518")
		}); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, err := runCheck("--json", dir)
	if err == nil {
		t.Fatalf("check reported no error for duplicate ISBNs (stderr: %s)", stderr)
	}
	var issues []check.Issue
	if err := json.Unmarshal([]byte(stdout), &issues); err != nil {
		t.Fatalf("check JSON: %v\n%s", err, stdout)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "duplicate ISBN") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no duplicate ISBN issue in %+v", issues)
	}
}
