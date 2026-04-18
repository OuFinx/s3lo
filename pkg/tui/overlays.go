package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ConfirmDialog asks the user to confirm a destructive action.
type ConfirmDialog struct {
	title   string
	message string
	action  string // passed through in confirmMsg
	target  string // s3Ref for delete, "" for clean
}

func newConfirmDialog(title, message, action, target string) ConfirmDialog {
	return ConfirmDialog{title: title, message: message, action: action, target: target}
}

func (d ConfirmDialog) Init() tea.Cmd { return nil }

func (d ConfirmDialog) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "enter":
			return d, func() tea.Msg { return confirmMsg{action: d.action, target: d.target} }
		case "n", "esc", "q":
			return d, func() tea.Msg { return dismissOverlayMsg{} }
		}
	}
	return d, nil
}

func (d ConfirmDialog) View() string {
	return overlayStyle.Render(
		titleStyle.Render(d.title) + "\n\n" +
			d.message + "\n\n" +
			dimStyle.Render("[y/enter] confirm  [n/esc] cancel"),
	)
}

// InspectView displays a tag's manifest JSON.
type InspectView struct {
	content string
	loading bool
	scroll  int
}

func newInspectView() InspectView {
	return InspectView{loading: true}
}

func (v InspectView) Init() tea.Cmd { return nil }

func (v InspectView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case inspectResultMsg:
		v.loading = false
		if msg.err == nil {
			v.content = msg.content
		}
		return v, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "i":
			return v, func() tea.Msg { return dismissOverlayMsg{} }
		case "up", "k":
			if v.scroll > 0 {
				v.scroll--
			}
		case "down", "j":
			v.scroll++
		}
	}
	return v, nil
}

func (v InspectView) View() string {
	if v.loading {
		return overlayStyle.Render(titleStyle.Render("Inspect") + "\n\n  Loading...")
	}
	lines := strings.Split(v.content, "\n")
	if v.scroll >= len(lines) {
		v.scroll = len(lines) - 1
	}
	if v.scroll < 0 {
		v.scroll = 0
	}
	visible := lines[v.scroll:]
	if len(visible) > 30 {
		visible = visible[:30]
	}
	return overlayStyle.Render(
		titleStyle.Render("Inspect") + "\n\n" +
			strings.Join(visible, "\n") + "\n\n" +
			dimStyle.Render("[↑↓] scroll  [esc/i] close"),
	)
}

// ScanResultsView shows scan status. Trivy runs via tea.ExecProcess; this overlay
// just shows loading / completion state.
type ScanResultsView struct {
	done bool
	err  error
}

func newScanResultsView() ScanResultsView {
	return ScanResultsView{}
}

func (v ScanResultsView) Init() tea.Cmd { return nil }

func (v ScanResultsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case scanResultMsg:
		v.done = true
		v.err = msg.err
		return v, nil
	case tea.KeyMsg:
		if v.done || msg.String() == "esc" || msg.String() == "q" {
			return v, func() tea.Msg { return dismissOverlayMsg{} }
		}
	}
	return v, nil
}

func (v ScanResultsView) View() string {
	if !v.done {
		return overlayStyle.Render(
			titleStyle.Render("Scan") + "\n\n  Preparing image for Trivy...\n\n" +
				dimStyle.Render("[esc] cancel"),
		)
	}
	if v.err != nil {
		return overlayStyle.Render(
			titleStyle.Render("Scan") + "\n\n" +
				redStyle.Render("Error: "+v.err.Error()) + "\n\n" +
				dimStyle.Render("[any key] close"),
		)
	}
	return overlayStyle.Render(
		titleStyle.Render("Scan") + "\n\n" +
			greenStyle.Render("Scan complete.") + "\n\n" +
			dimStyle.Render("[any key] close"),
	)
}

// CleanPreviewView shows a dry-run summary and asks for confirmation.
type CleanPreviewView struct {
	loading bool
	preview CleanPreview
	err     error
}

func newCleanPreviewView() CleanPreviewView {
	return CleanPreviewView{loading: true}
}

func (v CleanPreviewView) Init() tea.Cmd { return nil }

func (v CleanPreviewView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case cleanPreviewFetchedMsg:
		v.loading = false
		v.err = msg.err
		v.preview = msg.preview
		return v, nil
	case tea.KeyMsg:
		if v.loading {
			return v, nil
		}
		switch msg.String() {
		case "y", "enter":
			return v, func() tea.Msg { return confirmMsg{action: "clean"} }
		case "n", "esc":
			return v, func() tea.Msg { return dismissOverlayMsg{} }
		}
	}
	return v, nil
}

func (v CleanPreviewView) View() string {
	if v.loading {
		return overlayStyle.Render(titleStyle.Render("Clean Preview") + "\n\n  Computing...")
	}
	if v.err != nil {
		return overlayStyle.Render(
			titleStyle.Render("Clean Preview") + "\n\n" +
				redStyle.Render("Error: "+v.err.Error()) + "\n\n" +
				dimStyle.Render("[esc] close"),
		)
	}
	p := v.preview
	body := fmt.Sprintf(
		"  Tags to prune:   %d\n  Blobs to free:   %d  (%s)\n",
		p.TagsPruned, p.BlobsFreed, formatBytes(p.FreedBytes),
	)
	return overlayStyle.Render(
		titleStyle.Render("Clean Preview") + "\n\n" +
			body + "\n" +
			dimStyle.Render("[y/enter] run clean  [n/esc] cancel"),
	)
}

// dismissOverlayMsg is sent by overlays when the user closes them.
type dismissOverlayMsg struct{}
