package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/stack"
)

type viewBranch struct {
	Name      string    `json:"name"`
	Parent    string    `json:"parent"`
	Base      string    `json:"base,omitempty"`
	Head      string    `json:"head,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	Commits   int       `json:"commits"`
	Merged    bool      `json:"merged"`
	IsCurrent bool      `json:"isCurrent"`
	MR        *stack.MR `json:"mr,omitempty"`
	time      string
}

type viewData struct {
	Trunk    string       `json:"trunk"`
	Remote   string       `json:"remote"`
	Current  string       `json:"current"`
	Branches []viewBranch `json:"branches"` // bottom first
}

func newViewCmd(app *App) *cobra.Command {
	var short, asJSON bool
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Show the current stack",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runView(short, asJSON)
		},
	}
	cmd.Flags().BoolVarP(&short, "short", "s", false, "one line per branch")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func (a *App) runView(short, asJSON bool) error {
	f, err := a.load()
	if err != nil {
		return err
	}
	s, cur, err := a.currentStack(f)
	if err != nil {
		return err
	}
	d, err := a.collectView(s, cur)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}
	if short {
		return a.printShort(a.Out, d)
	}
	return a.withPager(func(w io.Writer) error { return a.printLong(w, d) })
}

func (a *App) collectView(s *stack.Stack, cur string) (*viewData, error) {
	g := a.repo()
	d := &viewData{Trunk: s.Trunk, Remote: s.Remote, Current: cur}
	for _, b := range s.Branches {
		vb := viewBranch{Name: b.Name, Parent: s.Parent(b.Name), Base: b.Base, Merged: b.Merged, IsCurrent: b.Name == cur, MR: b.MR}
		if c, err := g.LastCommit(b.Name); err == nil {
			vb.Head, vb.Subject, vb.time = c.SHA, c.Subject, relTime(a.Now(), c.Time)
		}
		if !b.Merged {
			if n, err := g.CommitCount(vb.Parent, b.Name); err == nil {
				vb.Commits = n
			}
		}
		d.Branches = append(d.Branches, vb)
	}
	return d, nil
}

func (a *App) mrLabel(mr *stack.MR) string {
	if mr == nil {
		return ""
	}
	label := fmt.Sprintf("!%d", mr.IID)
	if mr.URL != "" && a.hyperlinks() {
		return osc8(mr.URL, label)
	}
	return label
}

func (a *App) printShort(w io.Writer, d *viewData) error {
	for i := len(d.Branches) - 1; i >= 0; i-- {
		b := d.Branches[i]
		line := marker(b) + " " + b.Name
		if mr := a.mrLabel(b.MR); mr != "" {
			line += " " + mr
		}
		if b.Merged {
			line += " (merged)"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, "  "+d.Trunk)
	return nil
}

func (a *App) printLong(w io.Writer, d *viewData) error {
	fmt.Fprintf(w, "Stack on %s (%d branches)\n\n", d.Trunk, len(d.Branches))
	for i := len(d.Branches) - 1; i >= 0; i-- {
		b := d.Branches[i]
		head := b.Name
		if b.IsCurrent {
			head += " (current)"
		}
		if b.Merged {
			head += " (merged)"
		}
		if mr := a.mrLabel(b.MR); mr != "" {
			head += "  " + mr
			if b.MR.URL != "" && !a.hyperlinks() {
				head += " " + b.MR.URL
			}
		}
		fmt.Fprintf(w, "%s %s\n", marker(b), head)
		if b.Head != "" {
			detail := fmt.Sprintf("%s %s · %s", b.Head[:7], b.Subject, b.time)
			if !b.Merged {
				detail += fmt.Sprintf(" · %s", plural(b.Commits, "commit"))
			}
			fmt.Fprintf(w, "│   %s\n", detail)
		}
	}
	fmt.Fprintf(w, "└ %s\n", d.Trunk)
	return nil
}

func marker(b viewBranch) string {
	switch {
	case b.IsCurrent:
		return "●"
	case b.Merged:
		return "✓"
	default:
		return "○"
	}
}
