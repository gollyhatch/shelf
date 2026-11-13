package cli

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gollyhatch/shelf/internal/epub"
)

func runModify(args ...string) (stdout, stderr string, err error) {
	cmd := modifyCmd(".")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestModifyTitleWithRegex(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)

	if _, stderr, err := runModify("--title", "-e", "Darkness", "--replacement", "Night", path); err != nil {
		t.Fatalf("modify title: %v (stderr: %s)", err, stderr)
	}
	m, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "The Left Hand of Night" {
		t.Errorf("Title = %q", m.Title)
	}
}

func TestModifyReplacementCapture(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)

	if _, stderr, err := runModify(
		"--title", "-e", `^(The) (Left)`, "--replacement", `$2 $1`, path,
	); err != nil {
		t.Fatalf("modify title: %v (stderr: %s)", err, stderr)
	}
	m, _ := epub.ReadMetadata(path)
	if m.Title != "Left The Hand of Darkness" {
		t.Errorf("Title = %q", m.Title)
	}
}

func TestModifyAuthorsAndTagsPerItem(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)
	if err := epub.Update(path, func(p *epub.Package) error {
		if err := p.SetCreators([]epub.Creator{
			{Name: "Le Guin, Ursula K."},
			{Name: "Jane Doe"},
		}); err != nil {
			return err
		}
		return p.SetSubjects([]string{"sf", "fantasy"})
	}); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := runModify(
		"--author", "-e", "Le Guin", "--replacement", "LeGuin", path,
	); err != nil {
		t.Fatalf("modify authors: %v (stderr: %s)", err, stderr)
	}
	if _, stderr, err := runModify(
		"--tags", "-e", "^sf$", "--replacement", "Science Fiction", path,
	); err != nil {
		t.Fatalf("modify tags: %v (stderr: %s)", err, stderr)
	}

	m, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.CreatorNames(); !slices.Equal(got, []string{"LeGuin, Ursula K.", "Jane Doe"}) {
		t.Errorf("creators = %v", got)
	}
	if !slices.Equal(m.Subjects, []string{"Science Fiction", "fantasy"}) {
		t.Errorf("subjects = %v", m.Subjects)
	}
}

func TestModifyDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := runModify(
		"--dry-run", "--title", "-e", "Darkness", "--replacement", "Night", path,
	); err != nil {
		t.Fatalf("modify dry run: %v (stderr: %s)", err, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the dry run rewrote the file")
	}
	m, _ := epub.ReadMetadata(path)
	if m.Title != "The Left Hand of Darkness" {
		t.Errorf("Title = %q", m.Title)
	}
}

func TestModifyNoMatchDoesNotRewrite(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := runModify(
		"--title", "-e", "no such text", "--replacement", "replacement", path,
	); err != nil {
		t.Fatalf("modify with no match: %v (stderr: %s)", err, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a no-match modify rewrote the file")
	}
}

func TestModifyErrors(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)

	if _, _, err := runModify("-e", "x", path); err == nil {
		t.Error("modify accepted a run without field flags")
	}
	if _, _, err := runModify("--title", path); err == nil {
		t.Error("modify accepted a run without a regex")
	}
	_, _, err := runModify("--title", "-e", "[", path)
	if err == nil || !strings.Contains(err.Error(), "invalid --regex") {
		t.Errorf("invalid regex error = %v", err)
	}
}
