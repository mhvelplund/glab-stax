package cli

import (
	tea "github.com/charmbracelet/bubbletea"
)

// scripted returns a RunTUI that feeds keys to the model instead of a terminal.
func scripted(ks ...string) func(tea.Model) (tea.Model, error) {
	return func(m tea.Model) (tea.Model, error) {
		for _, k := range ks {
			m, _ = m.Update(keyMsg(k))
		}
		return m, nil
	}
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case "ctrl+b":
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	case "shift+down":
		return tea.KeyMsg{Type: tea.KeyShiftDown}
	case "shift+up":
		return tea.KeyMsg{Type: tea.KeyShiftUp}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}
