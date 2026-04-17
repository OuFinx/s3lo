package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TagListPane shows the list of tags for a selected image, sorted newest-first.
type TagListPane struct {
	imageName string
	entries   []TagEntry
	cursor    int
	loading   bool
	spinner   spinner.Model
}

func newTagListPane(imageName string) TagListPane {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorBorder)
	return TagListPane{imageName: imageName, loading: true, spinner: s}
}

func (p TagListPane) Init() tea.Cmd { return p.spinner.Tick }

func (p TagListPane) Update(msg tea.Msg) (leftPane, tea.Cmd) {
	switch msg := msg.(type) {
	case tagsFetchedMsg:
		if msg.imageName != p.imageName {
			return p, nil
		}
		p.loading = false
		if msg.err == nil {
			p.entries = msg.tags
			p.cursor = 0
		}
		return p, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if p.cursor > 0 {
				p.cursor--
			}
		case "down", "j":
			if p.cursor < len(p.entries)-1 {
				p.cursor++
			}
		}
		return p, nil
	case spinner.TickMsg:
		if p.loading {
			var cmd tea.Cmd
			p.spinner, cmd = p.spinner.Update(msg)
			return p, cmd
		}
	}
	return p, nil
}

func (p TagListPane) View(width, height int) string {
	title := titleStyle.Render(fmt.Sprintf("%s — Tags (%d)", p.imageName, len(p.entries)))
	if p.loading {
		return title + "\n\n  " + p.spinner.View() + " Loading..."
	}
	if len(p.entries) == 0 {
		return title + "\n\n" + dimStyle.Render("  No tags found.")
	}
	var sb strings.Builder
	sb.WriteString(title + "\n\n")
	for i, e := range p.entries {
		line := fmt.Sprintf("%-20s  %8s  %s", e.Name, formatBytes(e.TotalBytes), formatAge(e.LastModified))
		if i == p.cursor {
			row := selectedStyle.Width(width - 4).Render("▶ " + line)
			sb.WriteString("  " + row + "\n")
		} else {
			sb.WriteString("  " + line + "\n")
		}
	}
	return sb.String()
}

func (p TagListPane) SelectedImageName() string { return p.imageName }

func (p TagListPane) SelectedTagName() string {
	if p.loading || len(p.entries) == 0 {
		return ""
	}
	return p.entries[p.cursor].Name
}

// formatAge renders a time.Time as a human-readable age relative to now.
func formatAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
