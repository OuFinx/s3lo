package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorBorder = lipgloss.Color("#58a6ff")
	colorDim    = lipgloss.Color("#8b949e")
	colorGreen  = lipgloss.Color("#3fb950")
	colorYellow = lipgloss.Color("#ffa657")
	colorRed    = lipgloss.Color("#f85149")
	colorSelect = lipgloss.Color("#79c0ff")
	colorSelBg  = lipgloss.Color("#1f3a5f")

	selectedStyle = lipgloss.NewStyle().
			Background(colorSelBg).
			Foreground(colorSelect)

	dimStyle    = lipgloss.NewStyle().Foreground(colorDim)
	greenStyle  = lipgloss.NewStyle().Foreground(colorGreen)
	yellowStyle = lipgloss.NewStyle().Foreground(colorYellow)
	redStyle    = lipgloss.NewStyle().Foreground(colorRed)

	titleStyle     = lipgloss.NewStyle().Foreground(colorBorder).Bold(true)
	statusErrStyle = lipgloss.NewStyle().Foreground(colorRed)
	statusOKStyle  = lipgloss.NewStyle().Foreground(colorDim)

	overlayStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2)
)
