package tui

import "charm.land/lipgloss/v2"

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("63")).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	bodyStyle = lipgloss.NewStyle().Padding(0, 1)

	selStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("24")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Foreground(lipgloss.Color("252"))

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true).
			Padding(1, 2)
)
