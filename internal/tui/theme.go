package tui

import "charm.land/lipgloss/v2"

// Basic ANSI colors follow the user's terminal palette, so they read on light and dark themes.
var (
	colorAccent = lipgloss.Color("6")
	colorOK     = lipgloss.Color("2")
	colorWarn   = lipgloss.Color("3")
	colorBad    = lipgloss.Color("1")
	colorDim    = lipgloss.Color("8")
)

var (
	styleAccent   = lipgloss.NewStyle().Foreground(colorAccent)
	styleBold     = lipgloss.NewStyle().Bold(true)
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleOK       = lipgloss.NewStyle().Foreground(colorOK)
	styleWarn     = lipgloss.NewStyle().Foreground(colorWarn)
	styleBad      = lipgloss.NewStyle().Foreground(colorBad)
	styleDim      = lipgloss.NewStyle().Foreground(colorDim)
	styleSelected = lipgloss.NewStyle().Reverse(true)
	styleKey      = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
)

func delayStyle(delay int) lipgloss.Style {
	switch {
	case delay <= 0:
		return styleDim
	case delay < 200:
		return styleOK
	case delay < 500:
		return styleWarn
	default:
		return styleBad
	}
}
