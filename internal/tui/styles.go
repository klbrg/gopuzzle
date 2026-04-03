package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorGreen  = lipgloss.Color("#00d787")
	colorRed    = lipgloss.Color("#ff5f5f")
	colorYellow = lipgloss.Color("#ffd700")
	colorBlue   = lipgloss.Color("#5f87ff")
	colorGray   = lipgloss.Color("#626262")
	colorWhite  = lipgloss.Color("#e4e4e4")
	colorPurple = lipgloss.Color("#af87ff")

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorBlue)

	styleConcept = lipgloss.NewStyle().
			Foreground(colorPurple).
			Bold(true)

	styleDifficulty = lipgloss.NewStyle().
			Foreground(colorYellow)

	styleDescription = lipgloss.NewStyle().
				Foreground(colorWhite)

	stylePass = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorGreen)

	styleFail = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorRed)

	styleHint = lipgloss.NewStyle().
			Foreground(colorYellow).
			Italic(true)

	styleExplanation = lipgloss.NewStyle().
				Foreground(colorGreen).
				Italic(true)

	styleOutput = lipgloss.NewStyle().
			Foreground(colorGray)

	styleKeybind = lipgloss.NewStyle().
			Foreground(colorGray)

	styleKeyName = lipgloss.NewStyle().
			Foreground(colorWhite).
			Bold(true)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorGray).
			Padding(0, 1)

	styleHeader = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(colorGray).
			Padding(0, 1).
			MarginBottom(1)

	stylePenalty = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)

	styleScore = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)
)

func difficultyStars(d int) string {
	stars := ""
	for i := 0; i < 10; i++ {
		if i < d {
			stars += "★"
		} else {
			stars += "☆"
		}
	}
	return stars
}
