package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// palette is one theme's adaptive colour set.
type palette struct {
	accent    lipgloss.AdaptiveColor
	text      lipgloss.AdaptiveColor
	subtle    lipgloss.AdaptiveColor
	faint     lipgloss.AdaptiveColor
	ok        lipgloss.AdaptiveColor
	err       lipgloss.AdaptiveColor
	selBg     lipgloss.AdaptiveColor
	selAuthor lipgloss.AdaptiveColor
	series    lipgloss.AdaptiveColor
	selSeries lipgloss.AdaptiveColor
}

func adaptive(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

// defaultTheme is used when no theme is configured.
const defaultTheme = "gruvbox"

// themes holds the built-in palettes.
var themes = map[string]palette{
	"ayu dark": {
		accent:    adaptive("#ff9940", "#e6b450"),
		text:      adaptive("#5c6166", "#bfbdb6"),
		subtle:    adaptive("#8a9199", "#a0a6ad"),
		faint:     adaptive("#b7bdc6", "#565b66"),
		ok:        adaptive("#86b300", "#aad94c"),
		err:       adaptive("#f07171", "#f07178"),
		selBg:     adaptive("#e7eaed", "#1e2530"),
		selAuthor: adaptive("#fa8d3e", "#ffb454"),
		series:    adaptive("#399ee6", "#59c2ff"),
		selSeries: adaptive("#a37acc", "#d2a6ff"),
	},
	"gruvbox": {
		accent:    adaptive("#427b58", "#8ec07c"),
		text:      adaptive("#3c3836", "#fbf1c7"),
		subtle:    adaptive("#504945", "#d5c4a1"),
		faint:     adaptive("#7c6f64", "#a89984"),
		ok:        adaptive("#79740e", "#b8bb26"),
		err:       adaptive("#9d0006", "#fb4934"),
		selBg:     adaptive("#e0d8b0", "#3c3836"),
		selAuthor: adaptive("#5f8a5b", "#b6d2a0"),
		series:    adaptive("#5a735a", "#9aa88a"),
		selSeries: adaptive("#6f8a63", "#c9d79a"),
	},
	"dracula": {
		accent:    adaptive("#6f42c1", "#bd93f9"),
		text:      adaptive("#282a36", "#f8f8f2"),
		subtle:    adaptive("#44475a", "#d7d7e0"),
		faint:     adaptive("#6272a4", "#6272a4"),
		ok:        adaptive("#1a7f37", "#50fa7b"),
		err:       adaptive("#d73a49", "#ff5555"),
		selBg:     adaptive("#d7d7e0", "#44475a"),
		selAuthor: adaptive("#b35c00", "#ffb86c"),
		series:    adaptive("#006a8a", "#8be9fd"),
		selSeries: adaptive("#a12f8f", "#ff79c6"),
	},
	"catppuccin": {
		accent:    adaptive("#8839ef", "#cba6f7"),
		text:      adaptive("#4c4f69", "#cdd6f4"),
		subtle:    adaptive("#5c5f77", "#bac2de"),
		faint:     adaptive("#8c8fa1", "#7f849c"),
		ok:        adaptive("#40a02b", "#a6e3a1"),
		err:       adaptive("#d20f39", "#f38ba8"),
		selBg:     adaptive("#ccd0da", "#45475a"),
		selAuthor: adaptive("#fe640b", "#fab387"),
		series:    adaptive("#04a5e5", "#89dceb"),
		selSeries: adaptive("#7287fd", "#b4befe"),
	},
}

var (
	colAccent    lipgloss.AdaptiveColor
	colText      lipgloss.AdaptiveColor
	colSubtle    lipgloss.AdaptiveColor
	colFaint     lipgloss.AdaptiveColor
	colOK        lipgloss.AdaptiveColor
	colErr       lipgloss.AdaptiveColor
	colSelBg     lipgloss.AdaptiveColor
	colSelAuthor lipgloss.AdaptiveColor
	colSeries    lipgloss.AdaptiveColor
	colSelSeries lipgloss.AdaptiveColor

	brandStyle           lipgloss.Style
	headerInfoStyle      lipgloss.Style
	searchLabelStyle     lipgloss.Style
	keyStyle             lipgloss.Style
	keySepStyle          lipgloss.Style
	paneStyle            lipgloss.Style
	paneActiveStyle      lipgloss.Style
	paneTitleStyle       lipgloss.Style
	paneTitleActiveStyle lipgloss.Style
	itemStyle            lipgloss.Style
	itemSubStyle         lipgloss.Style
	itemAuthorStyle      lipgloss.Style
	itemSeriesStyle      lipgloss.Style
	itemSelStyle         lipgloss.Style
	itemSelBarStyle      lipgloss.Style
	itemSelAuthorStyle   lipgloss.Style
	itemSelSeriesStyle   lipgloss.Style
	labelStyle           lipgloss.Style
	valueStyle           lipgloss.Style
	emptyStyle           lipgloss.Style
	rowSelStyle          lipgloss.Style
	addStyle             lipgloss.Style
	checkStyle           lipgloss.Style
	okStyle              lipgloss.Style
	errStyle             lipgloss.Style
	badStyle             lipgloss.Style
	editorBoxStyle       lipgloss.Style
	confirmBoxStyle      lipgloss.Style
	confirmTitleStyle    lipgloss.Style
	helpBoxStyle         lipgloss.Style
	helpTitleStyle       lipgloss.Style
	helpGroupStyle       lipgloss.Style
	editorLabelStyle     lipgloss.Style
	editorHintStyle      lipgloss.Style
	helpStyle            lipgloss.Style

	activeTheme = defaultTheme
)

func init() {
	if err := ApplyTheme(""); err != nil {
		panic(err)
	}
}

// themeOrder is the cycle used by the runtime theme switch.
var themeOrder = []string{"gruvbox", "ayu dark", "dracula", "catppuccin"}

// cycleTheme advances to the next built-in theme and restyles the open editors.
func (m *model) cycleTheme() {
	index := 0
	for i, name := range themeOrder {
		if name == ActiveTheme() {
			index = i
			break
		}
	}
	next := themeOrder[(index+1)%len(themeOrder)]
	if err := ApplyTheme(next); err != nil {
		m.setStatus("theme: "+err.Error(), statusErr)
		return
	}
	m.restyleEditors()
	m.setStatus("theme: "+next, statusInfo)
}

// ApplyTheme switches to a named built-in theme. An empty name selects the
// default, gruvbox.
func ApplyTheme(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = defaultTheme
	}
	p, ok := themes[name]
	if !ok {
		return fmt.Errorf("unknown theme %q (want %s)", name, themeNameList())
	}
	colAccent, colText, colSubtle = p.accent, p.text, p.subtle
	colFaint, colOK, colErr = p.faint, p.ok, p.err
	colSelBg, colSelAuthor = p.selBg, p.selAuthor
	colSeries, colSelSeries = p.series, p.selSeries
	activeTheme = name
	buildStyles()
	return nil
}

// ActiveTheme returns the currently applied theme name.
func ActiveTheme() string {
	return activeTheme
}

func themeNameList() string {
	names := make([]string, 0, len(themes))
	for name := range themes {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func buildStyles() {
	brandStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(colAccent).
		Padding(0, 1)

	headerInfoStyle = lipgloss.NewStyle().Foreground(colSubtle)

	searchLabelStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(colAccent)

	keyStyle = lipgloss.NewStyle().Foreground(colText).Bold(true)
	keySepStyle = lipgloss.NewStyle().Foreground(colFaint)

	paneStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colFaint).
		Padding(0, 1)
	paneActiveStyle = paneStyle.BorderForeground(colAccent)

	paneTitleStyle = lipgloss.NewStyle().Foreground(colSubtle)
	paneTitleActiveStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	itemStyle = lipgloss.NewStyle().Foreground(colText)
	itemSubStyle = lipgloss.NewStyle().Foreground(colSubtle)
	itemAuthorStyle = lipgloss.NewStyle().Foreground(colFaint)
	itemSeriesStyle = lipgloss.NewStyle().Foreground(colSeries)
	itemSelStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	itemSelBarStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	itemSelAuthorStyle = lipgloss.NewStyle().Foreground(colSelAuthor)
	itemSelSeriesStyle = lipgloss.NewStyle().Foreground(colSelSeries)

	labelStyle = lipgloss.NewStyle().Foreground(colSubtle)
	valueStyle = lipgloss.NewStyle().Foreground(colText)
	emptyStyle = lipgloss.NewStyle().Foreground(colFaint).Italic(true)
	rowSelStyle = lipgloss.NewStyle().Background(colSelBg).Bold(true)
	addStyle = lipgloss.NewStyle().Foreground(colFaint)
	checkStyle = lipgloss.NewStyle().Foreground(colOK).Bold(true)

	okStyle = lipgloss.NewStyle().Foreground(colOK)
	errStyle = lipgloss.NewStyle().Foreground(colErr)
	badStyle = lipgloss.NewStyle().Foreground(colErr).Bold(true)

	editorBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colAccent).
		Padding(0, 1)

	confirmBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colErr).
		Padding(0, 1)
	confirmTitleStyle = lipgloss.NewStyle().Foreground(colErr).Bold(true)

	helpBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colAccent).
		Padding(0, 1)
	helpTitleStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	helpGroupStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	editorLabelStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	editorHintStyle = lipgloss.NewStyle().Foreground(colSubtle)

	helpStyle = lipgloss.NewStyle().Foreground(colSubtle)
}

// markup paints a single key binding.
func markup(key, desc string) string {
	return keyStyle.Render(key) + " " + helpStyle.Render(desc)
}
