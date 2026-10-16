package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tyutyutyu/dodu/pkg/export"
	"github.com/tyutyutyu/dodu/pkg/group"
	"github.com/tyutyutyu/dodu/pkg/plan"
	"github.com/tyutyutyu/dodu/pkg/scan"
	"github.com/tyutyutyu/dodu/pkg/size"
)

func (m *Model) selectedNode() *group.Node {
	f := m.top()
	if f == nil || f.node == nil || f.selected < 0 || f.selected >= len(f.node.Children) {
		return nil
	}
	return f.node.Children[f.selected]
}

func nodeMark(n *group.Node) (plan.Mark, bool) {
	if n == nil {
		return plan.Mark{}, false
	}
	id := n.Meta["id"]
	switch n.Kind {
	case group.KindVolume:
		id = n.Name
	case group.KindImage, group.KindContainer, group.KindBuildCache:
	default:
		return plan.Mark{}, false
	}
	return plan.Mark{Kind: n.Kind, ID: id}, id != ""
}

func (m *Model) restoreSelection(old *group.Node) {
	mark, ok := nodeMark(old)
	if !ok {
		return
	}
	var visit func(*group.Node, []*frame) bool
	visit = func(n *group.Node, path []*frame) bool {
		for i, child := range n.Children {
			next := append(append([]*frame{}, path...), &frame{node: n, selected: i})
			if candidate, valid := nodeMark(child); valid && candidate == mark {
				m.stack = next
				return true
			}
			if visit(child, next) {
				return true
			}
		}
		return false
	}
	if m.root != nil {
		visit(m.root, nil)
	}
}

func (m *Model) toggleMark() {
	mark, ok := nodeMark(m.selectedNode())
	if !ok {
		m.status = "Open a group and mark an individual Docker object"
		return
	}
	if m.marks == nil {
		m.marks = make(map[plan.Mark]bool)
	}
	if m.marks[mark] {
		delete(m.marks, mark)
	} else {
		m.marks[mark] = true
	}
	m.status = fmt.Sprintf("%d objects marked; p previews the guarded plan", len(m.marks))
}

func (m *Model) openPlan() {
	if m.snap == nil {
		return
	}
	marks := make([]plan.Mark, 0, len(m.marks))
	// Walk in display order for a stable preview, including objects in other groups.
	seen := make(map[plan.Mark]bool)
	m.root.Walk(func(n *group.Node) {
		if mark, ok := nodeMark(n); ok && m.marks[mark] && !seen[mark] {
			marks = append(marks, mark)
			seen[mark] = true
		}
	})
	m.preview = plan.Build(m.snap, marks)
	m.previewOffset = 0
}

func (m *Model) plannerView() string {
	p := m.preview
	lines := []string{"Cleanup preview — dry-run", fmt.Sprintf("Estimated reclaim: %s; %d allowed, %d blocked", size.Format(p.EstReclaim, size.IEC), len(p.Items), len(p.Blocked))}
	for _, it := range p.Items {
		lines = append(lines, "DELETE "+string(it.Kind)+" "+it.Name+" (~"+size.Format(it.EstReclaim, size.IEC)+")")
	}
	for _, it := range p.Blocked {
		lines = append(lines, "BLOCKED "+it.Name+": "+it.Reason)
	}
	for _, warning := range p.Warnings {
		lines = append(lines, "WARNING "+warning)
	}
	height := max(1, m.height-3)
	start := min(m.previewOffset, max(0, len(lines)-height))
	end := min(len(lines), start+height)
	footer := "j/k scroll · Esc close · x refresh and prepare execution"
	if m.readOnly || os.Getenv("DODU_READONLY") == "1" {
		footer = "j/k scroll · Esc close · Read-only: execution disabled"
	}
	if m.preparing {
		footer = "Refreshing Docker state before confirmation…"
	}
	if m.confirming {
		footer = "Type yes then Enter to delete the allowed items above: " + m.confirmation + " · ↑/↓ scroll · Esc cancel"
	}
	if m.executing {
		footer = "Executing guarded cleanup…"
	}
	if m.quitAfterOperation {
		footer = "Cancelling operation; waiting for cleanup and audit to finish…"
	}
	return lipgloss.NewStyle().MaxWidth(max(1, m.width)).Render(strings.Join(lines[start:end], "\n") + "\n\n" + footer)
}

func (m *Model) detailsView() string {
	n := m.selectedNode()
	lines := []string{"Details"}
	if n != nil {
		lines = append(lines, n.Name, "Kind: "+string(n.Kind), "Total: "+size.Format(n.Size.Total, size.IEC), "Exclusive: "+size.Format(n.Size.Exclusive, size.IEC), "Shared: "+size.Format(n.Size.Shared, size.IEC))
		if n.Size.Estimated {
			lines = append(lines, "Size is estimated / unavailable")
		}
		id := n.Meta["id"]
		if m.snap != nil {
			for _, im := range m.snap.Images {
				if n.Kind == group.KindImage && im.ID == id {
					lines = append(lines, "ID: "+id, "Tags: "+strings.Join(im.RepoTags, ", "), "Created: "+im.Created.String())
				}
			}
			for _, c := range m.snap.Containers {
				if (n.Kind == group.KindContainer && c.ID == id) || (n.Kind == group.KindLogFile && c.ID == n.Meta["container_id"]) {
					lines = append(lines, "ID: "+c.ID, "Image: "+c.Image, "State: "+c.State, "Status: "+c.Status, "Log: "+c.LogPath, "Log size: "+size.Format(m.snap.LogSize(c.ID), size.IEC))
					for _, mount := range c.Mounts {
						lines = append(lines, "Mount: "+mount.Type+" "+mount.Source+" → "+mount.Destination)
					}
				}
			}
			for _, v := range m.snap.Volumes {
				if n.Kind == group.KindVolume && v.Name == n.Name {
					lines = append(lines, "Driver: "+v.Driver, "Mountpoint: "+v.Mountpoint)
				}
			}
			for _, b := range m.snap.BuildCache {
				if n.Kind == group.KindBuildCache && b.ID == id {
					lines = append(lines, "ID: "+id, "Type: "+b.Type, "Parents: "+strings.Join(b.Parents, ", "), "Last used: "+b.LastUsedAt.String())
				}
			}
		}
		if len(n.Refs.ContainerIDs) > 0 {
			lines = append(lines, "Used by: "+strings.Join(n.Refs.ContainerIDs, ", "))
		}
	}
	if m.snap != nil {
		for _, err := range m.snap.Errors {
			lines = append(lines, "Scan warning: "+err.Error())
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) breakdownView() string {
	t := size.ComputeTotals(m.snap)
	values := []int64{t.Images, t.Containers, t.Volumes, t.BuildCache, t.Logs}
	labels := []string{"Images", "Containers", "Volumes", "BuildCache", "Logs"}
	colors := []string{"63", "70", "214", "170", "39"}
	var total float64
	for _, v := range values {
		if v > 0 {
			total += float64(v)
		}
	}
	width := max(1, m.width-2)
	var bar strings.Builder
	var legend []string
	for i, v := range values {
		cells := 0
		if total > 0 && v > 0 {
			cells = int(float64(v) / total * float64(width))
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[i]))
		bar.WriteString(style.Render(strings.Repeat("█", cells)))
		legend = append(legend, style.Render(labels[i]+" "+size.Format(v, size.IEC)))
	}
	return bar.String() + "\n" + strings.Join(legend, " · ")
}

type exportDoneMsg struct {
	path string
	err  error
}

func (m *Model) exportCmd() tea.Cmd {
	snap := m.snap
	if snap == nil {
		return nil
	}
	return func() tea.Msg {
		f, err := os.CreateTemp(".", "dodu-snapshot-*.json")
		if err != nil {
			return exportDoneMsg{err: err}
		}
		path := f.Name()
		err = export.ToJSON(f, snap, export.ToolInfo{Name: "dodu", Version: "dev"})
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
		}
		return exportDoneMsg{path: path, err: err}
	}
}

type prepareDoneMsg struct {
	snap *scan.Snapshot
	err  error
}
type executeDoneMsg struct {
	report plan.Report
	err    error
}

func (m *Model) prepareCmd() tea.Cmd {
	ctx, cancel := context.WithTimeout(m.scanContext(), 30*time.Second)
	m.operationCancel = cancel
	return func() tea.Msg {
		defer cancel()
		snap, err := m.scanner.Scan(ctx)
		if err == nil && len(snap.Errors) > 0 {
			err = fmt.Errorf("incomplete scan: %v", snap.Errors)
		}
		return prepareDoneMsg{snap: snap, err: err}
	}
}

func (m *Model) executeCmd() tea.Cmd {
	p := m.preview
	ctx, cancel := context.WithTimeout(m.scanContext(), 5*time.Minute)
	m.operationCancel = cancel
	return func() tea.Msg {
		defer cancel()
		audit, err := plan.DefaultAuditLogPath()
		if err != nil {
			return executeDoneMsg{err: err}
		}
		report, err := p.Execute(ctx, m.client, plan.ExecOptions{AuditLog: audit, ReadOnly: m.readOnly})
		return executeDoneMsg{report: report, err: err}
	}
}
