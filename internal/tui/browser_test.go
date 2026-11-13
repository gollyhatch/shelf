package tui

import (
	"errors"
	"strings"
	"testing"
)

func TestExpandSearchURL(t *testing.T) {
	e := fakeEntry("/b/a.epub", "The Left Hand", "Le Guin, Ursula K.", "Hainish")
	e.Metadata.Subjects = []string{"Science Fiction", "Winter"}
	e.Metadata.SeriesIndex = "4"
	e.Metadata.Publisher = "Ace Books"
	e.Metadata.Date = "1969"
	e.Metadata.Language = "en"
	e.Metadata.Rights = "Copyright"
	e.Metadata.Identifier = "urn:isbn:1"

	pattern := "https://example.com/?t=%t&a=%a&g=%g&s=%s&e=%e&p=%p&d=%d&l=%l&r=%r&i=%i" +
		"&title=%title&author=%author&x=100%25&y=%20"
	want := "https://example.com/?t=The+Left+Hand&a=Le+Guin%2C+Ursula+K." +
		"&g=Science+Fiction%2C+Winter&s=Hainish&e=4&p=Ace+Books&d=1969&l=en" +
		"&r=Copyright&i=urn%3Aisbn%3A1&title=The+Left+Hand&author=Le+Guin%2C+Ursula+K." +
		"&x=100%25&y=%20"
	if got := expandSearchURL(pattern, e); got != want {
		t.Errorf("expandSearchURL =\n%q\nwant\n%q", got, want)
	}
}

func TestOpenSearchURL(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m.searchURL = "https://example.com/?q=%t&author=%a"

	var opened string
	m.openBrowser = func(target string) error {
		opened = target
		return nil
	}

	m, cmd := step(t, m, keyMsg("ctrl+k"))
	if cmd == nil {
		t.Fatal("ctrl+k did not schedule a browser launch")
	}
	m = drain(t, m, cmd)

	want := expandSearchURL(m.searchURL, m.entries[0])
	if opened != want {
		t.Errorf("opened %q, want %q", opened, want)
	}
	if !strings.Contains(want, "The+Left+Hand+of+Darkness") {
		t.Errorf("expanded URL lost the title: %q", want)
	}
}

func TestOpenSearchURLRequiresConfig(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m.searchURL = ""
	m.openBrowser = func(string) error {
		t.Fatal("browser launched without a configured URL")
		return nil
	}

	if _, cmd := step(t, m, keyMsg("ctrl+k")); cmd != nil {
		t.Fatal("ctrl+k scheduled a command without a configured URL")
	}
}

func TestBrowserErrorBecomesStatus(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m.searchURL = "https://example.com/"
	m.openBrowser = func(string) error { return errors.New("no browser") }

	m, cmd := step(t, m, keyMsg("ctrl+k"))
	if cmd == nil {
		t.Fatal("ctrl+k did not schedule a browser launch")
	}
	m = drain(t, m, cmd)
	if !strings.Contains(m.status, "browser: no browser") {
		t.Errorf("status = %q", m.status)
	}
}
