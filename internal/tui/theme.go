package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type Theme struct {
	Base      lipgloss.Style
	Title     lipgloss.Style
	Subtle    lipgloss.Style
	Rule      lipgloss.Style
	TabActive lipgloss.Style
	TabIdle   lipgloss.Style
	Header    lipgloss.Style
	Row       lipgloss.Style
	RowAlt    lipgloss.Style
	Cursor    lipgloss.Style
	Good      lipgloss.Style
	Warn      lipgloss.Style
	Bad       lipgloss.Style
	Muted     lipgloss.Style
	Accent    lipgloss.Style
	Help      lipgloss.Style
	KeyHint   lipgloss.Style

	BarFilled color.Color
	BarEmpty  color.Color
	GoodColor color.Color
	WarnColor color.Color
	BadColor  color.Color
}

func NewTheme(isDark bool) Theme {
	c := lipgloss.LightDark(isDark)

	text := c(lipgloss.Color("#1c1c1c"), lipgloss.Color("#e6e6e6"))
	muted := c(lipgloss.Color("#6b6b6b"), lipgloss.Color("#8a8a8a"))
	faint := c(lipgloss.Color("#a0a0a0"), lipgloss.Color("#5a5a5a"))
	accent := c(lipgloss.Color("#0a5cc7"), lipgloss.Color("#7aa2f7"))
	good := c(lipgloss.Color("#0b7a3b"), lipgloss.Color("#5fd68a"))
	warn := c(lipgloss.Color("#9a6400"), lipgloss.Color("#e0af68"))
	bad := c(lipgloss.Color("#b3261e"), lipgloss.Color("#f7768e"))
	cursorBg := c(lipgloss.Color("#dbe6fb"), lipgloss.Color("#2b3350"))

	return Theme{
		Base:      lipgloss.NewStyle().Foreground(text),
		Title:     lipgloss.NewStyle().Foreground(text).Bold(true),
		Subtle:    lipgloss.NewStyle().Foreground(muted),
		Rule:      lipgloss.NewStyle().Foreground(faint),
		TabActive: lipgloss.NewStyle().Foreground(accent).Bold(true).Underline(true),
		TabIdle:   lipgloss.NewStyle().Foreground(faint),
		Header:    lipgloss.NewStyle().Foreground(muted).Bold(true),
		Row:       lipgloss.NewStyle().Foreground(text),
		RowAlt:    lipgloss.NewStyle().Foreground(text),
		Cursor:    lipgloss.NewStyle().Foreground(text).Background(cursorBg).Bold(true),
		Good:      lipgloss.NewStyle().Foreground(good),
		Warn:      lipgloss.NewStyle().Foreground(warn),
		Bad:       lipgloss.NewStyle().Foreground(bad),
		Muted:     lipgloss.NewStyle().Foreground(muted),
		Accent:    lipgloss.NewStyle().Foreground(accent),
		Help:      lipgloss.NewStyle().Foreground(faint),
		KeyHint:   lipgloss.NewStyle().Foreground(muted).Bold(true),

		BarFilled: accent,
		BarEmpty:  faint,
		GoodColor: good,
		WarnColor: warn,
		BadColor:  bad,
	}
}

func (t Theme) ShareStyle(share float64) lipgloss.Style {
	switch {
	case share >= 0.95:
		return t.Good
	case share >= 0.6:
		return t.Warn
	default:
		return t.Bad
	}
}
