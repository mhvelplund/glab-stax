// Package tui holds the full-screen terminal editors.
package tui

import (
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tabStyle      = lipgloss.NewStyle().Padding(0, 1)
	activeTab     = tabStyle.Reverse(true)
	helpStyle     = dimStyle
)

// Run runs a model full-screen on in/out and returns the final model.
func Run(m tea.Model, in io.Reader, out io.Writer) (tea.Model, error) {
	// Styles detect colour support from stdout by default; draw for out instead,
	// which stays a terminal when stdout is captured (--print-path).
	lipgloss.SetColorProfile(lipgloss.NewRenderer(out).ColorProfile())
	return tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
}
