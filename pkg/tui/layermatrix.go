package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// LayerMatrixView is an overlay that renders a layer-sharing matrix for one image.
// Rows are unique content layers (sorted by share count then size); columns are tags.
// A filled cell (████) means that tag contains that layer; a dot cell (····) means it does not.
type LayerMatrixView struct {
	imageName string
	loading   bool
	matrix    LayerMatrix
	err       error
	scrollY   int // index of the first visible layer row
	scrollX   int // index of the first visible tag column
	spinner   spinner.Model
}

func newLayerMatrixView(imageName string) LayerMatrixView {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorBorder)
	return LayerMatrixView{imageName: imageName, loading: true, spinner: s}
}

func (v LayerMatrixView) Init() tea.Cmd { return v.spinner.Tick }

func (v LayerMatrixView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case layerMatrixFetchedMsg:
		if msg.imageName != v.imageName {
			return v, nil
		}
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.matrix = msg.matrix
		}
		return v, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "g":
			return v, func() tea.Msg { return dismissOverlayMsg{} }
		case "up", "k":
			if v.scrollY > 0 {
				v.scrollY--
			}
		case "down", "j":
			if v.scrollY < len(v.matrix.Rows)-1 {
				v.scrollY++
			}
		case "left", "h":
			if v.scrollX > 0 {
				v.scrollX--
			}
		case "right", "l":
			if v.scrollX < len(v.matrix.Tags)-1 {
				v.scrollX++
			}
		}
		return v, nil

	case spinner.TickMsg:
		if v.loading {
			var cmd tea.Cmd
			v.spinner, cmd = v.spinner.Update(msg)
			return v, cmd
		}
	}
	return v, nil
}

// Layout constants for the matrix grid.
const (
	matrixDigestWidth = 20 // "sha256:abcdef012345…" (7 + 12 + 1 chars)
	matrixSizeWidth   = 8  // "100.0 MB" max
	matrixTagWidth    = 6  // fixed cell width per tag column
	matrixMaxVisRows  = 14 // layer rows shown without scrolling
	matrixMaxVisCols  = 6  // tag columns shown without scrolling
)

func (v LayerMatrixView) View() string {
	title := titleStyle.Render("Layer sharing — " + v.imageName)

	if v.loading {
		return overlayStyle.Render(
			title + "\n\n  " + v.spinner.View() + " Loading manifests...\n\n" +
				dimStyle.Render("[esc] cancel"),
		)
	}
	if v.err != nil {
		return overlayStyle.Render(
			title + "\n\n" + redStyle.Render("  Error: "+v.err.Error()) +
				"\n\n" + dimStyle.Render("[esc] close"),
		)
	}
	if len(v.matrix.Rows) == 0 {
		return overlayStyle.Render(
			title + "\n\n" +
				dimStyle.Render("  No layer data available.\n  (Image may use multi-arch image indexes.)\n\n[esc] close"),
		)
	}

	m := v.matrix

	// Clamp scroll positions.
	endX := v.scrollX + matrixMaxVisCols
	if endX > len(m.Tags) {
		endX = len(m.Tags)
	}
	endY := v.scrollY + matrixMaxVisRows
	if endY > len(m.Rows) {
		endY = len(m.Rows)
	}

	var sb strings.Builder
	sb.WriteString(title + "\n\n")

	// Header: "Layer" column + "Size" column + one column per visible tag.
	header := fmt.Sprintf("  %-*s  %-*s", matrixDigestWidth, "Layer", matrixSizeWidth, "Size")
	for _, tag := range m.Tags[v.scrollX:endX] {
		name := tag
		if len(name) > matrixTagWidth-1 {
			name = name[:matrixTagWidth-1] + "…"
		}
		header += fmt.Sprintf("  %-*s", matrixTagWidth, name)
	}
	sb.WriteString(dimStyle.Render(header) + "\n")

	// One data row per unique layer.
	for _, row := range m.Rows[v.scrollY:endY] {
		shortDigest := "sha256:" + row.Digest[:12] + "…"
		line := fmt.Sprintf("  %-*s  %-*s", matrixDigestWidth, shortDigest, matrixSizeWidth, formatBytes(row.Size))

		for i := v.scrollX; i < endX; i++ {
			if row.Present[i] {
				line += "  " + greenStyle.Render(fmt.Sprintf("%-*s", matrixTagWidth, "████"))
			} else {
				line += "  " + dimStyle.Render(fmt.Sprintf("%-*s", matrixTagWidth, "····"))
			}
		}

		// Annotate layers shared by multiple tags.
		if row.TagCount > 1 {
			line += "  " + dimStyle.Render(fmt.Sprintf("← %d tags", row.TagCount))
		}
		sb.WriteString(line + "\n")
	}

	// Summary line: unique layers, bytes stored, dedup savings.
	sb.WriteString("\n")
	summary := fmt.Sprintf("  %d unique layers · %s stored", len(m.Rows), formatBytes(m.StoredBytes))
	if m.LogicalBytes > m.StoredBytes {
		savePct := float64(m.LogicalBytes-m.StoredBytes) / float64(m.LogicalBytes) * 100
		summary += fmt.Sprintf(" · %s logical · %.0f%% dedup", formatBytes(m.LogicalBytes), savePct)
	}
	sb.WriteString(greenStyle.Render(summary) + "\n\n")

	// Navigation hints, shown only when scrolling is possible.
	var hints []string
	if len(m.Rows) > matrixMaxVisRows {
		hints = append(hints, "[↑↓] scroll layers")
	}
	if len(m.Tags) > matrixMaxVisCols {
		hints = append(hints, "[←→] scroll tags")
	}
	hints = append(hints, "[esc] close")
	sb.WriteString(dimStyle.Render("  " + strings.Join(hints, "  ")))

	return overlayStyle.Render(sb.String())
}
