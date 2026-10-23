package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keys(m tea.Model, ks ...string) tea.Model {
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m, _ = m.Update(msg)
	}
	return m
}

func TestPickerNavigateAndChoose(t *testing.T) {
	p := NewPicker("Pick", []Item{{Title: "c"}, {Title: "b"}, {Title: "a"}})
	p.Initial = 1
	keys(p, "j", "enter")
	if p.Chosen != 2 {
		t.Fatalf("Chosen = %d, want 2", p.Chosen)
	}
	p = NewPicker("Pick", []Item{{Title: "c"}, {Title: "b"}})
	keys(p, "down", "down", "down", "k", "enter")
	if p.Chosen != 0 {
		t.Fatalf("clamped Chosen = %d, want 0", p.Chosen)
	}
	p = NewPicker("Pick", []Item{{Title: "c"}})
	keys(p, "q")
	if p.Chosen != -1 {
		t.Fatal("q should cancel")
	}
}

func TestPickerSearchAndTabs(t *testing.T) {
	p := NewPicker("Pick", []Item{
		{Title: "local one", Tab: 1}, {Title: "remote api", Tab: 2}, {Title: "remote ui", Tab: 2},
	})
	p.Tabs = []string{"All", "Local", "Remote"}
	keys(p, "/", "u", "i", "enter", "enter")
	if p.Chosen != 2 {
		t.Fatalf("search Chosen = %d, want 2", p.Chosen)
	}
	p.Chosen = -1
	keys(p, "/", "esc", "tab", "enter") // clear search, Local tab
	if p.Chosen != 0 {
		t.Fatalf("Local tab Chosen = %d, want 0", p.Chosen)
	}
	p.Chosen = -1
	keys(p, "tab", "j", "enter") // Remote tab, second item
	if p.Chosen != 2 {
		t.Fatalf("Remote tab Chosen = %d, want 2", p.Chosen)
	}
	p.Chosen = -1
	keys(p, "/", "z", "z", "enter", "enter")
	if p.Chosen != -1 {
		t.Fatal("enter with no matches should not choose")
	}
	if v := p.View(); v == "" {
		t.Fatal("empty view")
	}
}
