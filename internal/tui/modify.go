package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// ModifyNode is one layer in the modify editor.
type ModifyNode struct {
	Name    string
	Orig    string   // original branch, "" for an inserted layer
	Sources []string // original layers whose commits this layer holds
	Commits int      // commit count of the original layer (for display)
	Dropped bool
}

// ModifyEditor stages structural changes to a stack. Nodes are bottom first.
type ModifyEditor struct {
	Nodes   []*ModifyNode
	Applied bool
	// Details returns text for the f (files) and c (commits) panels.
	Details func(branch, kind string) string

	cursor     int
	history    [][]*ModifyNode
	input      textinput.Model
	inputMode  string // "", "rename", "insert-below", "insert-above"
	detail     string
	help       bool
	confirming bool
	status     string
}

// NewModifyEditor returns an editor focused on the top layer.
func NewModifyEditor(nodes []*ModifyNode) *ModifyEditor {
	ti := textinput.New()
	ti.Prompt = "name: "
	ti.CharLimit = 200
	return &ModifyEditor{Nodes: nodes, cursor: len(nodes) - 1, input: ti}
}

func (e *ModifyEditor) Init() tea.Cmd { return nil }

func (e *ModifyEditor) snapshot() {
	cp := make([]*ModifyNode, len(e.Nodes))
	for i, n := range e.Nodes {
		c := *n
		c.Sources = append([]string(nil), n.Sources...)
		cp[i] = &c
	}
	e.history = append(e.history, cp)
}

func (e *ModifyEditor) changed() bool { return len(e.history) > 0 }

// liveBelow/liveAbove find the nearest non-dropped layer next to i.
func (e *ModifyEditor) liveBelow(i int) int {
	for j := i - 1; j >= 0; j-- {
		if !e.Nodes[j].Dropped {
			return j
		}
	}
	return -1
}

func (e *ModifyEditor) liveAbove(i int) int {
	for j := i + 1; j < len(e.Nodes); j++ {
		if !e.Nodes[j].Dropped {
			return j
		}
	}
	return -1
}

func (e *ModifyEditor) live() int {
	n := 0
	for _, x := range e.Nodes {
		if !x.Dropped {
			n++
		}
	}
	return n
}

func (e *ModifyEditor) remove(i int) {
	e.Nodes = append(e.Nodes[:i], e.Nodes[i+1:]...)
	if e.cursor >= len(e.Nodes) {
		e.cursor = len(e.Nodes) - 1
	}
}

func (e *ModifyEditor) nameTaken(name string) bool {
	for _, n := range e.Nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}

func (e *ModifyEditor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	e.status = ""
	if e.inputMode != "" {
		return e.handleInput(k)
	}
	if e.confirming {
		if k.String() == "y" || k.String() == "Y" {
			return e, tea.Quit
		}
		e.confirming = false
		return e, nil
	}
	if e.help {
		e.help = false
		return e, nil
	}
	n := e.Nodes[e.cursor]
	switch k.String() {
	case "ctrl+c":
		return e, tea.Quit
	case "q", "esc":
		if e.detail != "" {
			e.detail = ""
			return e, nil
		}
		if e.changed() {
			e.confirming = true
			return e, nil
		}
		return e, tea.Quit
	case "?":
		e.help = true
	case "up", "k":
		e.cursor = min(e.cursor+1, len(e.Nodes)-1)
		e.detail = ""
	case "down", "j":
		e.cursor = max(e.cursor-1, 0)
		e.detail = ""
	case "shift+up", "K":
		if e.cursor < len(e.Nodes)-1 {
			e.snapshot()
			e.Nodes[e.cursor], e.Nodes[e.cursor+1] = e.Nodes[e.cursor+1], e.Nodes[e.cursor]
			e.cursor++
		}
	case "shift+down", "J":
		if e.cursor > 0 {
			e.snapshot()
			e.Nodes[e.cursor], e.Nodes[e.cursor-1] = e.Nodes[e.cursor-1], e.Nodes[e.cursor]
			e.cursor--
		}
	case "x":
		if !n.Dropped && e.live() == 1 {
			e.status = "cannot drop the last layer"
			return e, nil
		}
		e.snapshot()
		n.Dropped = !n.Dropped
	case "d":
		below := e.liveBelow(e.cursor)
		if n.Dropped || below < 0 {
			e.status = "nothing below to fold into"
			return e, nil
		}
		e.snapshot()
		e.Nodes[below].Sources = append(e.Nodes[below].Sources, n.Sources...)
		e.status = fmt.Sprintf("folded %s into %s", n.Name, e.Nodes[below].Name)
		e.remove(e.cursor)
		e.cursor = below
	case "u":
		above := e.liveAbove(e.cursor)
		if n.Dropped || above < 0 {
			e.status = "nothing above to fold into"
			return e, nil
		}
		e.snapshot()
		e.Nodes[above].Sources = append(append([]string(nil), n.Sources...), e.Nodes[above].Sources...)
		e.status = fmt.Sprintf("folded %s into %s", n.Name, e.Nodes[above].Name)
		e.remove(e.cursor)
		e.cursor = above - 1
	case "r":
		e.startInput("rename", n.Name)
	case "i":
		e.startInput("insert-below", "")
	case "I":
		e.startInput("insert-above", "")
	case "z":
		if len(e.history) > 0 {
			e.Nodes = e.history[len(e.history)-1]
			e.history = e.history[:len(e.history)-1]
			e.cursor = min(e.cursor, len(e.Nodes)-1)
		}
	case "f", "c":
		if e.Details != nil && n.Orig != "" {
			kind := map[string]string{"f": "files", "c": "commits"}[k.String()]
			e.detail = e.Details(n.Orig, kind)
		}
	case "ctrl+s":
		if !e.changed() {
			e.status = "no changes to apply"
			return e, nil
		}
		e.Applied = true
		return e, tea.Quit
	}
	return e, nil
}

func (e *ModifyEditor) startInput(mode, value string) {
	e.inputMode = mode
	e.input.SetValue(value)
	e.input.CursorEnd()
	e.input.Focus()
}

func (e *ModifyEditor) handleInput(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		e.inputMode = ""
		return e, nil
	case tea.KeyEnter:
		name := strings.TrimSpace(e.input.Value())
		mode := e.inputMode
		e.inputMode = ""
		n := e.Nodes[e.cursor]
		switch {
		case name == "" || (mode == "rename" && name == n.Name):
			return e, nil
		case strings.ContainsAny(name, " \t~^:?*[\\") || strings.HasPrefix(name, "-"):
			e.status = fmt.Sprintf("%q is not a valid branch name", name)
			return e, nil
		case e.nameTaken(name):
			e.status = fmt.Sprintf("%q is already in the stack", name)
			return e, nil
		}
		e.snapshot()
		switch mode {
		case "rename":
			n.Name = name
		case "insert-below":
			e.Nodes = append(e.Nodes[:e.cursor], append([]*ModifyNode{{Name: name}}, e.Nodes[e.cursor:]...)...)
		case "insert-above":
			e.Nodes = append(e.Nodes[:e.cursor+1], append([]*ModifyNode{{Name: name}}, e.Nodes[e.cursor+1:]...)...)
			e.cursor++
		}
		return e, nil
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(k)
	return e, cmd
}

func (e *ModifyEditor) View() string {
	if e.help {
		return modifyHelp
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("glab stax modify") + "\n\n")
	for i := len(e.Nodes) - 1; i >= 0; i-- {
		n := e.Nodes[i]
		var notes []string
		switch {
		case n.Dropped:
			notes = append(notes, "dropped")
		case n.Orig == "":
			notes = append(notes, "new")
		case n.Name != n.Orig:
			notes = append(notes, "renamed from "+n.Orig)
		}
		if len(n.Sources) > 1 || (len(n.Sources) == 1 && n.Sources[0] != n.Orig) {
			notes = append(notes, "commits of "+strings.Join(n.Sources, " + "))
		}
		if n.Orig != "" {
			notes = append(notes, plural(n.Commits, "commit"))
		}
		line := n.Name
		if n.Dropped {
			line = dimStyle.Strikethrough(true).Render(n.Name)
		}
		if i == e.cursor {
			b.WriteString(selectedStyle.Render("› ") + selectedStyle.Render(line))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("  " + dimStyle.Render(strings.Join(notes, " · ")) + "\n")
	}
	b.WriteString("\n")
	if e.detail != "" {
		b.WriteString(e.detail + "\n\n")
	}
	switch {
	case e.inputMode != "":
		b.WriteString(e.input.View() + dimStyle.Render("  (enter to confirm, esc to cancel)") + "\n")
	case e.confirming:
		b.WriteString("Discard your changes? (y/N)\n")
	case e.status != "":
		b.WriteString(selectedStyle.Render(e.status) + "\n")
	default:
		b.WriteString(helpStyle.Render("x drop · d/u fold down/up · i/I insert · ⇧↓/⇧↑ move · r rename · z undo · ^s apply · ? help") + "\n")
	}
	return b.String()
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

const modifyHelp = `glab stax modify — keys

  ↑ / ↓ (or k / j)       move between layers
  x                      drop the layer (toggle): its commits leave the stack
  d                      fold the layer into the one below
  u                      fold the layer into the one above
  i / I                  insert an empty layer below / above
  shift+↓ / shift+↑      move the layer down / up (also J / K)
  r                      rename the layer
  z                      undo the last change
  f / c                  show the files changed / commits of the layer
  ^s                     apply all changes
  q / esc                quit (asks first if you changed something)

Dropped and folded branches keep their local branch and MR.
Press any key to close.`
