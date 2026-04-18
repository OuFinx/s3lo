package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// leftPane is implemented by ImageListPane and TagListPane.
type leftPane interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (leftPane, tea.Cmd)
	View(width, height int) string
	// SelectedImageName returns the image name under the cursor (empty if loading/empty).
	SelectedImageName() string
	// SelectedTagName returns the tag name under the cursor (empty in image-list mode).
	SelectedTagName() string
}

// ImageListPane shows the list of images in the bucket.
type ImageListPane struct {
	entries []ImageListEntry
	cursor  int
	loading bool
	spinner spinner.Model
}

func newImageListPane() ImageListPane {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorBorder)
	return ImageListPane{loading: true, spinner: s}
}

func (p ImageListPane) Init() tea.Cmd { return p.spinner.Tick }

func (p ImageListPane) Update(msg tea.Msg) (leftPane, tea.Cmd) {
	switch msg := msg.(type) {
	case imagesFetchedMsg:
		p.loading = false
		if msg.err == nil {
			p.entries = msg.entries
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

func (p ImageListPane) View(width, height int) string {
	title := titleStyle.Render(fmt.Sprintf("Images (%d)", len(p.entries)))
	if p.loading {
		return title + "\n\n  " + p.spinner.View() + " Loading..."
	}
	if len(p.entries) == 0 {
		return title + "\n\n" + dimStyle.Render("  No images found.")
	}
	var sb strings.Builder
	sb.WriteString(title + "\n\n")
	for i, e := range p.entries {
		line := fmt.Sprintf("%-28s  %3d tags  %s", e.Name, e.TagCount, formatBytes(e.TotalBytes))
		if i == p.cursor {
			row := selectedStyle.Width(width - 4).Render("▶ " + line)
			sb.WriteString("  " + row + "\n")
		} else {
			sb.WriteString("    " + line + "\n")
		}
	}
	return sb.String()
}

func (p ImageListPane) SelectedImageName() string {
	if p.loading || len(p.entries) == 0 {
		return ""
	}
	return p.entries[p.cursor].Name
}

func (p ImageListPane) SelectedTagName() string { return "" }

// formatCost renders a dollar amount; shows "<$0.01" instead of "$0.00" for tiny positive values.
func formatCost(v float64) string {
	if v > 0 && v < 0.005 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

// formatBytes renders byte counts as a human-readable string.
func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
