package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func press(m tea.Model, ks ...tea.KeyMsg) tea.Model {
	for _, k := range ks {
		m, _ = m.Update(k)
	}
	return m
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var (
	kEnter = tea.KeyMsg{Type: tea.KeyEnter}
	kTab   = tea.KeyMsg{Type: tea.KeyTab}
	kEsc   = tea.KeyMsg{Type: tea.KeyEsc}
	kSpace = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	kCtrlS = tea.KeyMsg{Type: tea.KeyCtrlS}
	kCtrlX = tea.KeyMsg{Type: tea.KeyCtrlX}
	kCtrlB = tea.KeyMsg{Type: tea.KeyCtrlB}
	kBack  = tea.KeyMsg{Type: tea.KeyBackspace}
)

func nodes() []*SubmitNode {
	return []*SubmitNode{
		{Branch: "a", Target: "main", IID: 1, State: "open", URL: "https://x/1"},
		{Branch: "b", Target: "a", Include: true, Title: "b title"},
		{Branch: "c", Target: "b", Include: true, Title: "c title"},
	}
}

func TestSubmitEditorEditAndSubmit(t *testing.T) {
	e := NewSubmitEditor(nodes())
	// Focused on c (top). Edit its title, then flip it to draft.
	press(e, kEnter, kBack, kBack, kBack, kBack, kBack, runes("TITLE"), kTab, runes("body text"), kTab, kSpace, kCtrlS)
	c := e.Nodes[2]
	if !e.Submitted || c.Title != "c TITLE" || c.Body != "body text" || !c.Draft {
		t.Fatalf("submitted=%v node=%+v", e.Submitted, c)
	}
}

func TestSubmitEditorSkipCascades(t *testing.T) {
	e := NewSubmitEditor(nodes())
	press(e, runes("j"), kSpace) // move to b and skip it: c is skipped too
	if e.Nodes[1].Include || e.Nodes[2].Include {
		t.Fatal("skip did not cascade upward")
	}
	press(e, runes("k"), kCtrlX) // include c: b is included too
	if !e.Nodes[1].Include || !e.Nodes[2].Include {
		t.Fatal("include did not cascade downward")
	}
	press(e, runes("j"), runes("j"), kSpace) // a has an MR: no change
	if !strings.Contains(e.View(), "!1 already exists") {
		t.Fatalf("view:\n%s", e.View())
	}
}

func TestSubmitEditorValidationAndQuit(t *testing.T) {
	e := NewSubmitEditor(nodes())
	press(e, kEnter)
	for range "c title" {
		press(e, kBack)
	}
	press(e, kCtrlS)
	if e.Submitted || !strings.Contains(e.View(), "c needs a title") {
		t.Fatal("empty title accepted")
	}
	press(e, kEsc, runes("q"))
	if e.Cancelled {
		t.Fatal("quit with edits should ask first")
	}
	press(e, runes("y"))
	if !e.Cancelled {
		t.Fatal("confirm did not cancel")
	}
}

func TestSubmitEditorLinkExisting(t *testing.T) {
	ns := []*SubmitNode{
		{Branch: "a", Target: "main", IID: 1, State: "open"},
		{Branch: "b", Target: "a", IID: 2, State: "draft"},
		{Branch: "c", Target: "b", Include: true, Title: "c"},
	}
	e := NewSubmitEditor(ns)
	press(e, kCtrlB)
	if e.Submitted {
		t.Fatal("^b allowed with a new MR selected")
	}
	press(e, kSpace, kCtrlB)
	if !e.Submitted || !e.LinkOnly {
		t.Fatal("^b did not link existing MRs")
	}
}

func TestSubmitEditorOpenURLAndHelp(t *testing.T) {
	e := NewSubmitEditor(nodes())
	var opened string
	e.OpenURL = func(u string) error { opened = u; return nil }
	press(e, runes("j"), runes("j"), tea.KeyMsg{Type: tea.KeyCtrlO})
	if opened != "https://x/1" {
		t.Fatalf("opened %q", opened)
	}
	press(e, runes("?"))
	if !strings.Contains(e.View(), "glab stax submit — keys") {
		t.Fatal("help not shown")
	}
	press(e, runes("x"))
	if strings.Contains(e.View(), "— keys") {
		t.Fatal("help not closed")
	}
}

func TestSubmitEditorKeepsUneditedBody(t *testing.T) {
	body := "| a | b |\r\n|---|---|\r\n\tcode"
	e := NewSubmitEditor([]*SubmitNode{{Branch: "a", Target: "main", Include: true, Title: "t", Body: body}})
	press(e, tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyEsc}, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !e.Submitted || e.Nodes[0].Body != body {
		t.Fatalf("body rewritten: %q", e.Nodes[0].Body)
	}
	// ^H in a field deletes instead of opening help.
	e = NewSubmitEditor([]*SubmitNode{{Branch: "a", Target: "main", Include: true, Title: "abc"}})
	press(e, tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyCtrlH})
	if e.help || e.title.Value() != "ab" {
		t.Fatalf("help %v, title %q", e.help, e.title.Value())
	}
}
