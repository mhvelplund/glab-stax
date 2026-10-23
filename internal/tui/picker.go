package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Item is one entry in a Picker.
type Item struct {
	Title  string
	Detail string
	Tab    int // index into Picker.Tabs; ignored without tabs
}

// Picker is a searchable list with optional tabs. The first tab, if any,
// shows every item.
type Picker struct {
	Title   string
	Tabs    []string
	Items   []Item
	Initial int // item index to start on

	Chosen    int // index into Items, -1 when cancelled
	tab       int
	cursor    int
	query     string
	searching bool
	started   bool
	height    int // terminal rows, 0 until known
}

// NewPicker returns a picker with nothing chosen yet.
func NewPicker(title string, items []Item) *Picker {
	return &Picker{Title: title, Items: items, Chosen: -1}
}

func (p *Picker) Init() tea.Cmd { return nil }

// visible returns the indexes of the items shown on the current tab and query.
func (p *Picker) visible() []int {
	var out []int
	q := strings.ToLower(p.query)
	for i, it := range p.Items {
		if len(p.Tabs) > 0 && p.tab > 0 && it.Tab != p.tab {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Title+" "+it.Detail), q) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !p.started {
		p.started = true
		for pos, i := range p.visible() {
			if i == p.Initial {
				p.cursor = pos
			}
		}
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		p.height = size.Height
		return p, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	vis := p.visible()
	if p.searching {
		switch key.Type {
		case tea.KeyEsc:
			p.searching, p.query = false, ""
		case tea.KeyEnter:
			p.searching = false
		case tea.KeyBackspace, tea.KeyCtrlH: // some terminals send ^H for backspace
			if r := []rune(p.query); len(r) > 0 {
				p.query = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			// bubbletea delivers a space as KeySpace carrying the rune.
			if len(key.Runes) > 0 {
				p.query += string(key.Runes)
			} else if key.Type == tea.KeySpace {
				p.query += " "
			}
		case tea.KeyUp, tea.KeyDown:
			p.move(key.Type == tea.KeyDown, len(vis))
		case tea.KeyCtrlC:
			return p, tea.Quit
		}
		p.clamp()
		return p, nil
	}
	switch key.String() {
	case "ctrl+c", "q", "esc":
		p.Chosen = -1
		return p, tea.Quit
	case "up", "k":
		p.move(false, len(vis))
	case "down", "j":
		p.move(true, len(vis))
	case "tab", "right", "l":
		if len(p.Tabs) > 0 {
			p.tab, p.cursor = (p.tab+1)%len(p.Tabs), 0
		}
	case "shift+tab", "left", "h":
		if len(p.Tabs) > 0 {
			p.tab, p.cursor = (p.tab+len(p.Tabs)-1)%len(p.Tabs), 0
		}
	case "/":
		p.searching, p.query = true, ""
	case "enter":
		if len(vis) > 0 {
			p.Chosen = vis[p.cursor]
			return p, tea.Quit
		}
	}
	return p, nil
}

// listRoom returns how many list rows fit on the screen, or 0 if unknown.
func (p *Picker) listRoom() int {
	if p.height == 0 {
		return 0
	}
	overhead := 4 // title, blank line, blank line, help
	if len(p.Tabs) > 0 {
		overhead += 2
	}
	return max(p.height-overhead, 1)
}

func (p *Picker) move(down bool, n int) {
	if n == 0 {
		return
	}
	if down {
		p.cursor = min(p.cursor+1, n-1)
	} else {
		p.cursor = max(p.cursor-1, 0)
	}
}

func (p *Picker) clamp() {
	n := len(p.visible())
	if p.cursor >= n {
		p.cursor = max(n-1, 0)
	}
}

func (p *Picker) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(p.Title) + "\n\n")
	if len(p.Tabs) > 0 {
		for i, t := range p.Tabs {
			if i == p.tab {
				b.WriteString(activeTab.Render(t))
			} else {
				b.WriteString(tabStyle.Render(t))
			}
		}
		b.WriteString("\n\n")
	}
	vis := p.visible()
	if len(vis) == 0 {
		b.WriteString(dimStyle.Render("  nothing matches") + "\n")
	}
	// Show only as many rows as fit, keeping the cursor in view: the terminal
	// would otherwise cut off the top of the list.
	first, last := 0, len(vis)
	if room := p.listRoom(); room > 0 && len(vis) > room {
		first = min(max(p.cursor-room/2, 0), len(vis)-room)
		last = first + room
	}
	for pos := first; pos < last; pos++ {
		it := p.Items[vis[pos]]
		line := it.Title
		if it.Detail != "" {
			line += "  " + dimStyle.Render(it.Detail)
		}
		if pos == p.cursor {
			b.WriteString(selectedStyle.Render("› ") + selectedStyle.Render(it.Title))
			if it.Detail != "" {
				b.WriteString("  " + dimStyle.Render(it.Detail))
			}
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if p.searching {
		fmt.Fprintf(&b, "/%s█\n", p.query)
	} else {
		help := "↑/↓ move · enter select · / search · q quit"
		if len(p.Tabs) > 0 {
			help = "↑/↓ move · tab switch tab · enter select · / search · q quit"
		}
		b.WriteString(helpStyle.Render(help) + "\n")
	}
	return b.String()
}
