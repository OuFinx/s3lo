package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type statsPanelMode int

const (
	statsBucket statsPanelMode = iota
	statsTag
)

// StatsPanel renders bucket-level or tag-level stats in the right pane.
type StatsPanel struct {
	mode        statsPanelMode
	bucketStats *BucketStats
	tagStats    *TagStats
	imageName   string
	tagName     string
	loading     bool
	local       bool // suppress cost rows for local:// backends
	spinner     spinner.Model
}

func newStatsPanel(local bool) StatsPanel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorBorder)
	return StatsPanel{mode: statsBucket, loading: true, local: local, spinner: s}
}

func (p StatsPanel) Init() tea.Cmd { return p.spinner.Tick }

func (p StatsPanel) Update(msg tea.Msg) (StatsPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case bucketStatsFetchedMsg:
		p.loading = false
		if msg.err == nil {
			p.bucketStats = &msg.stats
		}
		return p, nil
	case tagStatsFetchedMsg:
		if p.mode == statsTag && msg.cacheKey == p.imageName+":"+p.tagName {
			p.loading = false
			if msg.err == nil {
				p.tagStats = &msg.stats
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

// SetBucketMode switches the panel to bucket stats view.
func (p StatsPanel) SetBucketMode() StatsPanel {
	p.mode = statsBucket
	p.imageName = ""
	p.tagName = ""
	p.tagStats = nil
	p.loading = p.bucketStats == nil
	return p
}

// SetTagMode switches the panel to display stats for imageName:tagName.
func (p StatsPanel) SetTagMode(imageName, tagName string) StatsPanel {
	p.mode = statsTag
	p.imageName = imageName
	p.tagName = tagName
	p.tagStats = nil
	p.loading = true
	return p
}

// CacheKey returns the current "imageName:tagName" key, or "" in bucket mode.
func (p StatsPanel) CacheKey() string {
	if p.mode == statsTag {
		return p.imageName + ":" + p.tagName
	}
	return ""
}

func (p StatsPanel) View(width, height int) string {
	if p.mode == statsBucket {
		return p.viewBucket()
	}
	return p.viewTag()
}

func (p StatsPanel) viewBucket() string {
	title := titleStyle.Render("Bucket Stats")
	if p.loading || p.bucketStats == nil {
		return title + "\n\n  " + p.spinner.View() + " Loading..."
	}
	s := p.bucketStats
	var sb strings.Builder
	sb.WriteString(title + "\n\n")
	sb.WriteString(fmt.Sprintf("  Images:        %d\n", s.Images))
	sb.WriteString(fmt.Sprintf("  Tags:          %d\n", s.Tags))
	sb.WriteString(fmt.Sprintf("  Unique blobs:  %d\n\n", s.UniqueBlobs))
	sb.WriteString(fmt.Sprintf("  Total size:    %s\n", formatBytes(s.TotalBytes)))
	sb.WriteString(fmt.Sprintf("  Logical size:  %s\n", formatBytes(s.LogicalBytes)))
	if s.LogicalBytes > s.TotalBytes {
		saved := s.LogicalBytes - s.TotalBytes
		sb.WriteString(greenStyle.Render(fmt.Sprintf("  Dedup saved:   %s (%.1f%%)", formatBytes(saved), s.SavingsPct)) + "\n")
	}
	sb.WriteString("\n")
	if !p.local {
		sb.WriteString(yellowStyle.Render(fmt.Sprintf("  Est. cost:     %s/month", formatCost(s.CostMonthly))) + "\n")
		sb.WriteString(redStyle.Render(fmt.Sprintf("  ECR equiv:     %s/month", formatCost(s.ECRMonthly))) + "\n")
		if s.ECRMonthly > s.CostMonthly {
			saved := s.ECRMonthly - s.CostMonthly
			sb.WriteString(greenStyle.Render(fmt.Sprintf("  You save:      %s/month (%.0f%%)", formatCost(saved), saved/s.ECRMonthly*100)) + "\n")
		}
	}
	return sb.String()
}

func (p StatsPanel) viewTag() string {
	title := titleStyle.Render(fmt.Sprintf("%s:%s", p.imageName, p.tagName))
	if p.loading || p.tagStats == nil {
		return title + "\n\n  " + p.spinner.View() + " Loading..."
	}
	s := p.tagStats
	var sb strings.Builder
	sb.WriteString(title + "\n\n")
	sb.WriteString(fmt.Sprintf("  Size:    %s\n", formatBytes(s.TotalBytes)))
	sb.WriteString(fmt.Sprintf("  Layers:  %d\n", s.LayerCount))
	for _, pl := range s.Platforms {
		sb.WriteString(fmt.Sprintf("  Arch:    %s\n", pl))
	}
	if s.Signed {
		sb.WriteString(greenStyle.Render("  Signed:  ✔") + "\n")
	} else {
		sb.WriteString(dimStyle.Render("  Signed:  —") + "\n")
	}
	sb.WriteString("\n")
	if !p.local {
		sb.WriteString(yellowStyle.Render(fmt.Sprintf("  Cost:    %s/month", formatCost(s.CostMonthly))) + "\n")
	}
	return sb.String()
}
