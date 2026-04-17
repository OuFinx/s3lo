package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// RootModel is the top-level bubbletea model.
type RootModel struct {
	ctx       context.Context
	s3Ref     string // bucket-level ref, e.g. "s3://my-bucket/"
	storage   storage.Backend
	bucket    string
	prefix    string
	leftPane  leftPane
	right     StatsPanel
	overlay   tea.Model // nil when no overlay is active
	tagCache  map[string]TagStats // keyed by "imageName:tagName"
	status    string
	statusErr bool
	err       error // fatal error shown full-screen
	width     int
	height    int
}

// New creates a RootModel ready to start.
func New(ctx context.Context, s3Ref string, st storage.Backend, bucket, prefix string) RootModel {
	return RootModel{
		ctx:      ctx,
		s3Ref:    s3Ref,
		storage:  st,
		bucket:   bucket,
		prefix:   prefix,
		leftPane: newImageListPane(),
		right:    newStatsPanel(),
		tagCache: make(map[string]TagStats),
	}
}

func (m RootModel) Init() tea.Cmd {
	return tea.Batch(
		m.leftPane.Init(),
		m.right.Init(),
		fetchImagesCmd(m.ctx, m.storage, m.bucket, m.prefix),
		fetchBucketStatsCmd(m.ctx, m.s3Ref),
	)
}

func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Fatal error: only quit allowed.
	if m.err != nil {
		if key, ok := msg.(tea.KeyMsg); ok {
			if key.String() == "q" || key.String() == "ctrl+c" {
				return m, tea.Quit
			}
		}
		return m, nil
	}

	// Window resize.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = ws.Width
		m.height = ws.Height
		return m, nil
	}

	// Overlay control messages — handled at root level regardless of overlay state.
	switch msg := msg.(type) {
	case confirmMsg:
		m.overlay = nil
		return m.handleConfirm(msg)
	case dismissOverlayMsg:
		m.overlay = nil
		return m, nil
	}

	// If overlay active, route to overlay first.
	if m.overlay != nil {
		var cmd tea.Cmd
		m.overlay, cmd = m.overlay.Update(msg)
		// Also pass data messages (inspect results, scan results, clean preview) to overlay.
		switch msg.(type) {
		case inspectResultMsg, scanResultMsg, cleanPreviewFetchedMsg:
			// Already handled above via overlay.Update(msg)
		}
		return m, cmd
	}

	// Route messages.
	switch msg := msg.(type) {
	case imagesFetchedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		var cmd tea.Cmd
		m.leftPane, cmd = m.leftPane.Update(msg)
		return m, cmd

	case tagsFetchedMsg:
		if msg.err != nil {
			m = m.setStatus("could not load tags: "+msg.err.Error(), true)
			return m, clearStatusCmd()
		}
		var cmd tea.Cmd
		m.leftPane, cmd = m.leftPane.Update(msg)
		if len(msg.tags) > 0 {
			tagName := msg.tags[0].Name
			cacheKey := msg.imageName + ":" + tagName
			statsCmd := m.maybeLoadTagStats(msg.imageName, tagName, cacheKey)
			cmd = tea.Batch(cmd, statsCmd)
		}
		return m, cmd

	case tagStatsFetchedMsg:
		if msg.err == nil {
			m.tagCache[msg.cacheKey] = msg.stats
		}
		var cmd tea.Cmd
		m.right, cmd = m.right.Update(msg)
		return m, cmd

	case bucketStatsFetchedMsg:
		if msg.err != nil {
			m = m.setStatus("bucket stats unavailable: "+msg.err.Error(), true)
			return m, clearStatusCmd()
		}
		var cmd tea.Cmd
		m.right, cmd = m.right.Update(msg)
		return m, cmd

	case deleteResultMsg:
		if msg.err != nil {
			m = m.setStatus("delete failed: "+msg.err.Error(), true)
			return m, clearStatusCmd()
		}
		return m, m.refreshCmd()

	case cleanResultMsg:
		if msg.err != nil {
			m = m.setStatus("clean failed: "+msg.err.Error(), true)
			return m, clearStatusCmd()
		}
		m = m.setStatus(fmt.Sprintf("clean: %d items removed, %s freed", msg.deleted, formatBytes(msg.freed)), false)
		return m, tea.Batch(clearStatusCmd(), m.refreshCmd())

	case inspectResultMsg:
		if m.overlay != nil {
			m.overlay, _ = m.overlay.Update(msg)
		}
		return m, nil

	case cleanPreviewFetchedMsg:
		if m.overlay != nil {
			m.overlay, _ = m.overlay.Update(msg)
		}
		return m, nil

	case scanPreparedMsg:
		if msg.err != nil {
			m.overlay = nil
			m = m.setStatus("scan failed: "+msg.err.Error(), true)
			return m, clearStatusCmd()
		}
		trivyCmd := exec.Command(msg.trivyPath, "image", "--input", msg.tmpDir)
		tmpDir := msg.tmpDir
		return m, tea.ExecProcess(trivyCmd, func(err error) tea.Msg {
			os.RemoveAll(tmpDir)
			return scanResultMsg{err: err}
		})

	case scanResultMsg:
		if m.overlay != nil {
			m.overlay, _ = m.overlay.Update(msg)
		}
		return m, nil

	case statusClearMsg:
		m.status = ""
		m.statusErr = false
		return m, nil

	case spinner.TickMsg:
		var cmds []tea.Cmd
		var lCmd, rCmd tea.Cmd
		m.leftPane, lCmd = m.leftPane.Update(msg)
		m.right, rCmd = m.right.Update(msg)
		cmds = append(cmds, lCmd, rCmd)
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m RootModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "r":
		return m, m.refreshCmd()

	case "enter":
		imgName := m.leftPane.SelectedImageName()
		if imgName == "" {
			return m, nil
		}
		// Only drill in if we're in image-list mode (no tag selected yet at this level).
		if m.leftPane.SelectedTagName() != "" {
			return m, nil
		}
		m.leftPane = newTagListPane(imgName)
		m.right = m.right.SetTagMode(imgName, "")
		return m, tea.Batch(
			m.leftPane.Init(),
			fetchTagsCmd(m.ctx, m.storage, m.bucket, m.prefix, imgName),
		)

	case "esc":
		// Return to image list if in tag view.
		if m.leftPane.SelectedTagName() != "" || (m.leftPane.SelectedImageName() != "" && m.leftPane.SelectedTagName() == "") {
			// Check if we're a TagListPane by whether SelectedTagName could exist.
			// The TagListPane returns imageName from SelectedImageName, "" from SelectedTagName when loading.
			// We need to detect if we're in tag mode: TagListPane always has imageName set.
			// Simple heuristic: if SelectedImageName() returns non-empty and we're not in ImageListPane,
			// we need to go back. The ImageListPane.SelectedTagName always returns "".
			// TagListPane.SelectedImageName always returns the imageName.
			// But ImageListPane.SelectedImageName returns the selected entry name.
			// So we can't easily distinguish. Instead, track mode in the pane type check.
			// Simplest: try to go back — create a fresh ImageListPane and re-fetch.
		}
		m.leftPane = newImageListPane()
		m.right = m.right.SetBucketMode()
		return m, tea.Batch(
			m.leftPane.Init(),
			fetchImagesCmd(m.ctx, m.storage, m.bucket, m.prefix),
		)

	case "up", "down", "k", "j":
		var cmd tea.Cmd
		m.leftPane, cmd = m.leftPane.Update(msg)
		imgName := m.leftPane.SelectedImageName()
		tagName := m.leftPane.SelectedTagName()
		if imgName != "" && tagName != "" {
			cacheKey := imgName + ":" + tagName
			m.right = m.right.SetTagMode(imgName, tagName)
			cmd = tea.Batch(cmd, m.maybeLoadTagStats(imgName, tagName, cacheKey))
		}
		return m, cmd

	case "d":
		imgName := m.leftPane.SelectedImageName()
		if imgName == "" {
			return m, nil
		}
		tagName := m.leftPane.SelectedTagName()
		var targetRef, title, message string
		if tagName != "" {
			targetRef = strings.TrimSuffix(m.s3Ref, "/") + "/" + imgName + ":" + tagName
			title = "Delete tag"
			message = fmt.Sprintf("Delete %s:%s?", imgName, tagName)
		} else {
			targetRef = strings.TrimSuffix(m.s3Ref, "/") + "/" + imgName
			title = "Delete image"
			message = fmt.Sprintf("Delete all tags of %s?", imgName)
		}
		m.overlay = newConfirmDialog(title, message, "delete", targetRef)
		return m, nil

	case "i":
		imgName := m.leftPane.SelectedImageName()
		tagName := m.leftPane.SelectedTagName()
		if imgName == "" || tagName == "" {
			return m, nil
		}
		tagRef := strings.TrimSuffix(m.s3Ref, "/") + "/" + imgName + ":" + tagName
		m.overlay = newInspectView()
		return m, inspectTagCmd(m.ctx, tagRef)

	case "s":
		imgName := m.leftPane.SelectedImageName()
		tagName := m.leftPane.SelectedTagName()
		if imgName == "" || tagName == "" {
			return m, nil
		}
		tagRef := strings.TrimSuffix(m.s3Ref, "/") + "/" + imgName + ":" + tagName
		m.overlay = newScanResultsView()
		return m, prepareScanCmd(m.ctx, tagRef)

	case "c":
		m.overlay = newCleanPreviewView()
		return m, fetchCleanPreviewCmd(m.ctx, m.storage, m.bucket, m.s3Ref)
	}
	return m, nil
}

func (m RootModel) handleConfirm(msg confirmMsg) (RootModel, tea.Cmd) {
	switch msg.action {
	case "delete":
		return m, deleteTagCmd(m.ctx, msg.target)
	case "clean":
		return m, runCleanCmd(m.ctx, m.storage, m.bucket, m.s3Ref)
	}
	return m, nil
}

func (m RootModel) View() string {
	if m.err != nil {
		return "\n\n  " + redStyle.Render("Error: "+m.err.Error()) + "\n\n" +
			dimStyle.Render("  [q] quit") + "\n"
	}

	leftW := (m.width * 60) / 100
	rightW := m.width - leftW - 1
	if leftW <= 0 {
		leftW = 40
	}
	if rightW <= 0 {
		rightW = 20
	}

	leftView := m.leftPane.View(leftW, m.height-3)
	rightView := m.right.View(rightW, m.height-3)

	leftLines := strings.Split(leftView, "\n")
	rightLines := strings.Split(rightView, "\n")
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}
	sep := dimStyle.Render("│")
	var rows []string
	for i := 0; i < maxLines; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		l = lipgloss.NewStyle().Width(leftW).Render(l)
		rows = append(rows, l+sep+r)
	}
	main := strings.Join(rows, "\n")
	bar := m.renderStatusBar()

	if m.overlay != nil {
		type viewer interface{ View() string }
		if ov, ok := m.overlay.(viewer); ok {
			return main + "\n" + bar + "\n\n" + ov.View()
		}
	}

	return main + "\n" + bar
}

func (m RootModel) renderStatusBar() string {
	if m.status != "" {
		if m.statusErr {
			return statusErrStyle.Render("  " + m.status)
		}
		return statusOKStyle.Render("  " + m.status)
	}
	tagName := m.leftPane.SelectedTagName()
	if tagName != "" {
		return dimStyle.Render("  [↑↓] navigate  [esc] back  [d] delete  [i] inspect  [s] scan  [c] clean  [r] refresh  [q] quit")
	}
	return dimStyle.Render("  [↑↓] navigate  [enter] open  [d] delete  [c] clean  [r] refresh  [q] quit")
}

func (m RootModel) setStatus(msg string, isErr bool) RootModel {
	m.status = msg
	m.statusErr = isErr
	return m
}

func (m RootModel) refreshCmd() tea.Cmd {
	imgName := m.leftPane.SelectedImageName()
	if imgName != "" {
		// Could be either ImageListPane or TagListPane — check if we have a tag selected.
		tagName := m.leftPane.SelectedTagName()
		if tagName != "" || m.isInTagMode() {
			return tea.Batch(
				m.leftPane.Init(),
				fetchTagsCmd(m.ctx, m.storage, m.bucket, m.prefix, imgName),
			)
		}
	}
	return tea.Batch(
		fetchImagesCmd(m.ctx, m.storage, m.bucket, m.prefix),
		fetchBucketStatsCmd(m.ctx, m.s3Ref),
	)
}

// isInTagMode returns true if the left pane is a TagListPane.
// We detect this by checking if the pane is a TagListPane type.
func (m RootModel) isInTagMode() bool {
	_, ok := m.leftPane.(TagListPane)
	return ok
}

func (m RootModel) maybeLoadTagStats(imageName, tagName, cacheKey string) tea.Cmd {
	if cached, ok := m.tagCache[cacheKey]; ok {
		return func() tea.Msg {
			return tagStatsFetchedMsg{cacheKey: cacheKey, stats: cached}
		}
	}
	m.right = m.right.SetTagMode(imageName, tagName)
	return fetchTagStatsCmd(m.ctx, m.s3Ref, imageName, tagName)
}
