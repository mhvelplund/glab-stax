package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// SubmitNode is one branch in the submit editor.
type SubmitNode struct {
	Branch string
	Target string
	// Existing MR, if any (IID > 0). Existing MRs are always kept.
	IID   int
	URL   string
	State string // "open", "draft", ...

	// For new MRs: the editable draft.
	Include bool
	Title   string
	Body    string
	Draft   bool
}

func (n *SubmitNode) isNew() bool { return n.IID == 0 }

type submitFocus int

const (
	focusList submitFocus = iota
	focusTitle
	focusBody
	focusToggle
)

// SubmitEditor lets the user pick which branches become MRs and edit their
// title, description and draft state. Nodes are ordered bottom first.
type SubmitEditor struct {
	Nodes     []*SubmitNode
	Submitted bool
	Cancelled bool
	LinkOnly  bool // ^b: only link the existing MRs

	// OpenURL opens a URL in the browser (^o).
	OpenURL func(url string) error

	cursor int // index into Nodes
	focus  submitFocus
	title  textinput.Model
	body   textarea.Model
	// loadedTitle and loadedBody are what the fields showed when the focused
	// node was loaded. The fields rewrite their input (tabs, line endings), so
	// a node keeps its original text unless the field was actually edited.
	loadedTitle, loadedBody string
	help                    bool
	confirming              bool
	dirty                   bool
	status                  string
}

type editorDoneMsg struct {
	text string
	err  error
}

// NewSubmitEditor returns an editor focused on the topmost branch.
func NewSubmitEditor(nodes []*SubmitNode) *SubmitEditor {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 255
	ta := textarea.New()
	ta.SetHeight(8)
	ta.SetWidth(72)
	ta.ShowLineNumbers = false
	e := &SubmitEditor{Nodes: nodes, cursor: len(nodes) - 1, title: ti, body: ta, OpenURL: OpenBrowser}
	e.load()
	return e
}

func (e *SubmitEditor) Init() tea.Cmd { return nil }

func (e *SubmitEditor) node() *SubmitNode { return e.Nodes[e.cursor] }

// load copies the focused node into the text fields.
func (e *SubmitEditor) load() {
	if len(e.Nodes) == 0 {
		return
	}
	n := e.node()
	e.title.SetValue(n.Title)
	e.body.SetValue(strings.ReplaceAll(n.Body, "\r\n", "\n"))
	e.loadedTitle, e.loadedBody = e.title.Value(), e.body.Value()
}

// store copies edited text fields back into the focused node.
func (e *SubmitEditor) store() {
	n := e.node()
	if !n.isNew() {
		return
	}
	if v := e.title.Value(); v != e.loadedTitle {
		n.Title, e.loadedTitle, e.dirty = v, v, true
	}
	if v := e.body.Value(); v != e.loadedBody {
		n.Body, e.loadedBody, e.dirty = v, v, true
	}
}

func (e *SubmitEditor) setFocus(f submitFocus) {
	if !e.node().isNew() || !e.node().Include {
		f = focusList
	}
	e.focus = f
	e.title.Blur()
	e.body.Blur()
	switch f {
	case focusTitle:
		e.title.Focus()
	case focusBody:
		e.body.Focus()
	}
}

// toggleInclude flips the focused new branch. Skipping also skips the new
// branches above it; including also includes the new branches below it.
func (e *SubmitEditor) toggleInclude() {
	n := e.node()
	if !n.isNew() {
		e.status = fmt.Sprintf("!%d already exists", n.IID)
		return
	}
	n.Include = !n.Include
	e.dirty = true
	if n.Include {
		for i := e.cursor - 1; i >= 0; i-- {
			if e.Nodes[i].isNew() {
				e.Nodes[i].Include = true
			}
		}
	} else {
		for i := e.cursor + 1; i < len(e.Nodes); i++ {
			if e.Nodes[i].isNew() {
				e.Nodes[i].Include = false
			}
		}
		e.setFocus(focusList)
	}
}

func (e *SubmitEditor) included() int {
	n := 0
	for _, x := range e.Nodes {
		if x.isNew() && x.Include {
			n++
		}
	}
	return n
}

func (e *SubmitEditor) openMRs() int {
	n := 0
	for _, x := range e.Nodes {
		if !x.isNew() {
			n++
		}
	}
	return n
}

// canLinkExisting reports whether ^b applies: no new MRs selected and at
// least two existing ones.
func (e *SubmitEditor) canLinkExisting() bool { return e.included() == 0 && e.openMRs() >= 2 }

func (e *SubmitEditor) validate() string {
	for _, n := range e.Nodes {
		if n.isNew() && n.Include && strings.TrimSpace(n.Title) == "" {
			return fmt.Sprintf("%s needs a title", n.Branch)
		}
	}
	return ""
}

func (e *SubmitEditor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case editorDoneMsg:
		if msg.err != nil {
			e.status = "editor failed: " + msg.err.Error()
		} else if n := e.node(); n.isNew() {
			// Keep the editor's text as written; the field only displays it.
			n.Body, e.dirty = msg.text, true
			e.body.SetValue(strings.ReplaceAll(msg.text, "\r\n", "\n"))
			e.loadedBody = e.body.Value()
		}
		return e, nil
	case tea.KeyMsg:
		return e.handleKey(msg)
	}
	return e, nil
}

func (e *SubmitEditor) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	e.status = ""
	if e.confirming {
		switch k.String() {
		case "y", "Y":
			e.Cancelled = true
			return e, tea.Quit
		default:
			e.confirming = false
		}
		return e, nil
	}
	if e.help {
		e.help = false
		return e, nil
	}
	// Keys that work everywhere, including while typing.
	switch k.Type {
	case tea.KeyCtrlC:
		e.Cancelled = true
		return e, tea.Quit
	case tea.KeyCtrlS:
		e.store()
		if msg := e.validate(); msg != "" {
			e.status = msg
			return e, nil
		}
		if e.included() == 0 && e.openMRs() == 0 {
			e.status = "nothing selected"
			return e, nil
		}
		e.Submitted = true
		return e, tea.Quit
	case tea.KeyCtrlX:
		e.store()
		e.toggleInclude()
		return e, nil
	case tea.KeyCtrlO:
		if n := e.node(); n.URL != "" {
			if err := e.OpenURL(n.URL); err != nil {
				e.status = err.Error()
			}
		}
		return e, nil
	case tea.KeyCtrlE:
		if e.node().isNew() && e.node().Include {
			e.store()
			return e, editInEditor(e.body.Value())
		}
		return e, nil
	case tea.KeyCtrlB:
		if e.focus != focusBody && e.canLinkExisting() {
			e.LinkOnly, e.Submitted = true, true
			return e, tea.Quit
		}
	case tea.KeyTab:
		e.store()
		e.cycle(true)
		return e, nil
	case tea.KeyShiftTab:
		e.store()
		e.cycle(false)
		return e, nil
	}

	switch e.focus {
	case focusTitle:
		if k.Type == tea.KeyEnter {
			e.store()
			e.setFocus(focusBody)
			return e, nil
		}
		if k.Type == tea.KeyEsc {
			e.store()
			e.setFocus(focusList)
			return e, nil
		}
		var cmd tea.Cmd
		e.title, cmd = e.title.Update(k)
		e.store()
		return e, cmd
	case focusBody:
		if k.Type == tea.KeyEsc {
			e.store()
			e.setFocus(focusList)
			return e, nil
		}
		var cmd tea.Cmd
		e.body, cmd = e.body.Update(k)
		e.store()
		return e, cmd
	case focusToggle:
		switch k.String() {
		case " ", "left", "right", "h", "l":
			e.node().Draft = !e.node().Draft
			e.dirty = true
		case "esc":
			e.setFocus(focusList)
		}
		return e, nil
	}

	switch k.String() {
	case "up", "k":
		if e.cursor < len(e.Nodes)-1 {
			e.cursor++
			e.load()
		}
	case "down", "j":
		if e.cursor > 0 {
			e.cursor--
			e.load()
		}
	case " ":
		e.toggleInclude()
	case "enter":
		e.setFocus(focusTitle)
	case "?", "ctrl+h": // not while typing: some terminals send ^H for backspace
		e.help = true
	case "q", "esc":
		if e.dirty {
			e.confirming = true
			return e, nil
		}
		e.Cancelled = true
		return e, tea.Quit
	}
	return e, nil
}

// cycle moves through the fields of the focused branch, then on to the next
// branch up (or down with shift+tab).
func (e *SubmitEditor) cycle(forward bool) {
	editable := e.node().isNew() && e.node().Include
	if forward {
		if editable && e.focus < focusToggle {
			e.setFocus(e.focus + 1)
			return
		}
		if e.cursor < len(e.Nodes)-1 {
			e.cursor++
		} else {
			e.cursor = 0
		}
		e.load()
		e.setFocus(focusTitle)
		return
	}
	if editable && e.focus > focusTitle {
		e.setFocus(e.focus - 1)
		return
	}
	if e.cursor > 0 {
		e.cursor--
	} else {
		e.cursor = len(e.Nodes) - 1
	}
	e.load()
	e.setFocus(focusToggle)
}

func (e *SubmitEditor) View() string {
	if e.help {
		return submitHelp
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("glab stax submit") + dimStyle.Render(fmt.Sprintf("  %d new MR(s) selected", e.included())) + "\n\n")
	for i := len(e.Nodes) - 1; i >= 0; i-- {
		n := e.Nodes[i]
		box := "   "
		status := ""
		if n.isNew() {
			box = "[ ]"
			if n.Include {
				box = "[x]"
				status = "new"
				if n.Draft {
					status = "new draft"
				}
			} else {
				status = "skipped"
			}
		} else {
			status = fmt.Sprintf("!%d %s", n.IID, n.State)
		}
		line := fmt.Sprintf("%s %s → %s", box, n.Branch, n.Target)
		if i == e.cursor {
			b.WriteString(selectedStyle.Render("› "+line) + "  " + dimStyle.Render(status) + "\n")
		} else {
			b.WriteString("  " + line + "  " + dimStyle.Render(status) + "\n")
		}
	}
	b.WriteString("\n")
	n := e.node()
	switch {
	case !n.isNew():
		b.WriteString(dimStyle.Render(fmt.Sprintf("!%d already exists and will be kept up to date. ^o opens it.", n.IID)) + "\n")
	case !n.Include:
		b.WriteString(dimStyle.Render("Skipped: the branch is pushed but gets no MR. space or ^x includes it.") + "\n")
	default:
		b.WriteString(label("Title", e.focus == focusTitle) + "\n" + e.title.View() + "\n\n")
		b.WriteString(label("Description", e.focus == focusBody) + dimStyle.Render("  (^e opens $EDITOR)") + "\n" + e.body.View() + "\n\n")
		ready, draft := "( ) ready", "( ) draft"
		if n.Draft {
			draft = "(•) draft"
		} else {
			ready = "(•) ready"
		}
		b.WriteString(label("Create as", e.focus == focusToggle) + "  " + ready + "  " + draft + "\n")
	}
	b.WriteString("\n")
	switch {
	case e.confirming:
		b.WriteString("Discard your edits? (y/N)\n")
	case e.status != "":
		b.WriteString(selectedStyle.Render(e.status) + "\n")
	default:
		hint := "↑/↓ branch · space include · tab fields · ^s submit · ? help · q quit"
		if e.canLinkExisting() {
			hint += " · ^b link existing MRs"
		}
		b.WriteString(helpStyle.Render(hint) + "\n")
	}
	return b.String()
}

func label(s string, focused bool) string {
	if focused {
		return selectedStyle.Render(s)
	}
	return titleStyle.Render(s)
}

const submitHelp = `glab stax submit — keys

Choose which branches become MRs
  ↑ / ↓ (or k / j)   move between branches
  space / ^x         skip / include the focused branch (^x also while typing)
                     skipping also skips the branches above; including adds those below

Edit MR details
  enter / tab        edit the title, then description, then ready/draft
  shift+tab          go back
  space / ← / →      flip ready ↔ draft
  ^e                 edit the description in $EDITOR
  esc                stop editing

Existing MRs
  ^o                 open the focused branch's MR in the browser
  ^b                 link the existing MRs into a stack (when no new MR is selected)

Anywhere
  ^s                 submit
  ? / ^h             show this help (when not editing a field)
  q / esc            quit (asks first if you edited something)

Press any key to close.`

// editInEditor opens $VISUAL/$EDITOR on text and reports the result.
func editInEditor(text string) tea.Cmd {
	f, err := os.CreateTemp("", "glab-stax-*.md")
	if err != nil {
		return func() tea.Msg { return editorDoneMsg{err: err} }
	}
	path := f.Name()
	_, werr := f.WriteString(text)
	f.Close()
	if werr != nil {
		return func() tea.Msg { return editorDoneMsg{err: werr} }
	}
	editor := firstEnv("VISUAL", "EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorDoneMsg{err: err}
		}
		data, rerr := os.ReadFile(path)
		return editorDoneMsg{text: strings.TrimRight(string(data), "\n"), err: rerr}
	})
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// OpenBrowser opens url with the system's opener.
func OpenBrowser(url string) error {
	opener := "xdg-open"
	if _, err := exec.LookPath("open"); err == nil {
		opener = "open"
	}
	return exec.Command(opener, url).Start()
}
