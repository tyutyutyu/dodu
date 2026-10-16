// Package tui implements the Bubbletea-based interactive disk-atlas UI.
package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/group"
	"github.com/tyutyutyu/dodu/pkg/plan"
	"github.com/tyutyutyu/dodu/pkg/scan"
	"github.com/tyutyutyu/dodu/pkg/size"
)

// Layout is which root grouping the atlas shows.
type Layout int

// Available layouts.
const (
	LayoutByType Layout = iota
	LayoutByProject
)

// Model is the top-level Bubbletea model for dodu's TUI.
type Model struct {
	client      docker.Client
	readOnly    bool
	initialScan func(context.Context) (*scan.Snapshot, error)
	ctx         context.Context
	logger      *slog.Logger
	scanner     *scan.Scanner

	snap   *scan.Snapshot
	layout Layout
	sortBy group.SortKey
	root   *group.Node

	// Navigation: stack of "current node" frames. Top of stack is what we render.
	stack              []*frame
	help               bool
	hideDetails        bool
	marks              map[plan.Mark]bool
	preview            *plan.Plan
	previewOffset      int
	confirming         bool
	confirmation       string
	executing          bool
	preparing          bool
	operationCancel    context.CancelFunc
	quitAfterOperation bool

	width, height int
	loading       bool
	loadStarted   time.Time
	err           error
	status        string
}

type frame struct {
	node     *group.Node
	selected int
	offset   int
}

type scanDoneMsg struct {
	snap *scan.Snapshot
	err  error
}

// NewModel constructs a new TUI model.
func NewModel(client docker.Client, logger *slog.Logger) *Model {
	if logger == nil {
		logger = slog.Default()
	}
	return &Model{
		client:  client,
		logger:  logger,
		scanner: scan.New(client, logger),
		layout:  LayoutByType,
		sortBy:  group.SortBySize,
		loading: true,
	}
}

// Init kicks off the initial scan.
func (m *Model) Init() tea.Cmd {
	m.loadStarted = time.Now()
	if m.initialScan != nil {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.scanContext(), 30*time.Second)
			defer cancel()
			snap, err := m.initialScan(ctx)
			return scanDoneMsg{snap: snap, err: err}
		}
	}
	return m.scanCmd()
}

func (m *Model) scanCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.scanContext(), 30*time.Second)
		defer cancel()
		snap, err := m.scanner.Scan(ctx)
		return scanDoneMsg{snap: snap, err: err}
	}
}

func (m *Model) startScan() tea.Cmd {
	m.loading = true
	m.loadStarted = time.Now()
	m.err = nil
	m.status = "rescanning…"
	return m.scanCmd()
}

// Update handles input and async messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case scanDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.snap = msg.snap
		m.rebuildTree()
		return m, nil
	case prepareDoneMsg:
		m.preparing = false
		m.operationCancel = nil
		if m.quitAfterOperation {
			return m, tea.Quit
		}
		if msg.err != nil {
			m.status = "prepare cleanup: " + msg.err.Error()
			m.preview = nil
			return m, nil
		}
		m.snap = msg.snap
		m.rebuildTree()
		m.openPlan()
		m.confirming = true
		m.confirmation = ""
		return m, nil
	case executeDoneMsg:
		m.executing = false
		m.operationCancel = nil
		if m.quitAfterOperation {
			return m, tea.Quit
		}
		m.confirming = false
		m.preview = nil
		m.marks = nil
		m.status = fmt.Sprintf("Cleanup finished: %d results", len(msg.report.Results))
		if msg.err != nil {
			m.status += ": " + msg.err.Error()
		}
		m.loading = true
		return m, m.scanCmd()
	case exportDoneMsg:
		if msg.err != nil {
			m.status = "export: " + msg.err.Error()
		} else {
			m.status = "exported " + msg.path
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		if m.executing || m.preparing {
			m.quitAfterOperation = true
			if m.operationCancel != nil {
				m.operationCancel()
			}
			return m, nil
		}
		return m, tea.Quit
	}
	if m.executing || m.preparing {
		return m, nil
	}
	if m.loading || m.err != nil {
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "r":
			if !m.loading {
				return m, m.startScan()
			}
		}
		return m, nil
	}
	if m.help {
		switch msg.String() {
		case "?", "esc":
			m.help = false
		case "q":
			return m, tea.Quit
		}
		return m, nil
	}
	if m.confirming {
		switch msg.String() {
		case "down":
			m.previewOffset++
		case "up":
			if m.previewOffset > 0 {
				m.previewOffset--
			}
		case "esc":
			m.confirming = false
			m.confirmation = ""
		case "enter":
			if m.confirmation == "yes" {
				m.executing = true
				return m, m.executeCmd()
			}
		case "backspace":
			if len(m.confirmation) > 0 {
				m.confirmation = m.confirmation[:len(m.confirmation)-1]
			}
		default:
			if msg.Type == tea.KeyRunes && len(m.confirmation)+len(string(msg.Runes)) <= 3 {
				m.confirmation += string(msg.Runes)
			}
		}
		return m, nil
	}
	if m.preview != nil {
		switch msg.String() {
		case "x":
			if m.readOnly {
				m.status = "cleanup disabled: read-only"
				return m, nil
			}
			if len(m.preview.Items) > 0 {
				m.preparing = true
				return m, m.prepareCmd()
			}
		case "esc", "p":
			m.preview = nil
		case "j", "down":
			m.previewOffset++
		case "k", "up":
			if m.previewOffset > 0 {
				m.previewOffset--
			}
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case "r":
		return m, m.startScan()
	case "tab":
		m.hideDetails = !m.hideDetails
	case "d", " ":
		m.toggleMark()
	case "p":
		m.openPlan()
	case "e":
		return m, m.exportCmd()
	case "g":
		if m.layout == LayoutByType {
			m.layout = LayoutByProject
		} else {
			m.layout = LayoutByType
		}
		m.rebuildTree()
		return m, nil
	case "s":
		selected := m.selectedNode()
		switch m.sortBy {
		case group.SortBySize:
			m.sortBy = group.SortByName
		case group.SortByName:
			m.sortBy = group.SortByCount
		default:
			m.sortBy = group.SortBySize
		}
		if m.root != nil {
			m.root.Sort(m.sortBy)
			m.restoreSelection(selected)
		}
		return m, nil
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "enter", "l", "right":
		m.descend()
	case "h", "left", "esc", "backspace":
		m.ascend()
	case "g g", "home":
		f := m.top()
		if f != nil {
			f.selected = 0
			f.offset = 0
		}
	}
	return m, nil
}

func (m *Model) move(delta int) {
	f := m.top()
	if f == nil || f.node == nil {
		return
	}
	n := len(f.node.Children)
	if n == 0 {
		return
	}
	f.selected += delta
	if f.selected < 0 {
		f.selected = 0
	}
	if f.selected >= n {
		f.selected = n - 1
	}
}

func (m *Model) descend() {
	f := m.top()
	if f == nil || f.node == nil || len(f.node.Children) == 0 {
		return
	}
	child := f.node.Children[f.selected]
	if len(child.Children) == 0 {
		return
	}
	m.stack = append(m.stack, &frame{node: child})
}

func (m *Model) ascend() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
	}
}

func (m *Model) top() *frame {
	if len(m.stack) == 0 {
		return nil
	}
	return m.stack[len(m.stack)-1]
}

func (m *Model) rebuildTree() {
	if m.snap == nil {
		return
	}
	selected := m.selectedNode()
	if m.layout == LayoutByType {
		m.root = group.ByType(m.snap)
	} else {
		m.root = group.ByProject(m.snap)
	}
	m.root.Sort(m.sortBy)
	m.stack = []*frame{{node: m.root}}
	m.restoreSelection(selected)
}

// View renders the current state.
func (m *Model) View() string {
	if m.loading {
		return loadingView(m.loadStarted)
	}
	if m.err != nil {
		return errStyle.Render(fmt.Sprintf("scan failed: %v\n\nq quit  r retry", m.err))
	}
	if m.preview != nil {
		return m.plannerView()
	}
	if m.help {
		return helpView()
	}
	return m.atlasView()
}

func (m *Model) atlasView() string {
	header := m.headerView()
	footer := m.footerView()
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 5 {
		bodyHeight = 5
	}
	bodyHeight -= 2
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	body := m.listView(bodyHeight)
	if !m.hideDetails && m.width >= 80 {
		left := m.width / 2
		body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(left).MaxWidth(left).Render(body), lipgloss.NewStyle().Width(m.width-left).MaxWidth(m.width-left).MaxHeight(bodyHeight).Render(m.detailsView()))
	} else if m.width > 0 {
		body = lipgloss.NewStyle().MaxWidth(m.width).Render(body)
	}
	body = lipgloss.JoinVertical(lipgloss.Left, body, m.breakdownView())
	return lipgloss.NewStyle().MaxWidth(max(1, m.width)).MaxHeight(max(1, m.height)).Render(lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
}

func (m *Model) headerView() string {
	layout := "by-type"
	if m.layout == LayoutByProject {
		layout = "by-project"
	}
	sortName := []string{"size", "name", "count"}[m.sortBy]
	path := []string{}
	for _, f := range m.stack {
		if f.node != nil {
			path = append(path, f.node.Name)
		}
	}
	totals := ""
	if m.snap != nil && m.root != nil {
		totals = fmt.Sprintf("  total: %s", size.Format(m.root.Size.Total, size.IEC))
	}
	return headerStyle.Render(fmt.Sprintf("dodu  %s  sort:%s%s\n%s",
		layout, sortName, totals, strings.Join(path, " › ")))
}

func (m *Model) footerView() string {
	hint := "d mark  p plan  e export  Tab details  j/k move  l/enter open  h/esc back  g layout  s sort  r rescan  ? help  q quit"
	if m.status != "" {
		hint = m.status + "  •  " + hint
	}
	if m.snap != nil && len(m.snap.Errors) > 0 {
		hint = fmt.Sprintf("PARTIAL SCAN (%d errors; see details)  ", len(m.snap.Errors)) + hint
	}
	return footerStyle.Width(max(1, m.width-2)).Render(hint)
}

func (m *Model) listView(height int) string {
	f := m.top()
	if f == nil || f.node == nil || len(f.node.Children) == 0 {
		return bodyStyle.Render("(empty)")
	}
	children := f.node.Children
	// Adjust offset window to keep selected visible.
	if f.selected < f.offset {
		f.offset = f.selected
	}
	if f.selected >= f.offset+height {
		f.offset = f.selected - height + 1
	}
	if f.offset < 0 {
		f.offset = 0
	}
	end := f.offset + height
	if end > len(children) {
		end = len(children)
	}

	maxSize := int64(1)
	for _, c := range children {
		if c.Size.Exclusive > maxSize {
			maxSize = c.Size.Exclusive
		}
		if c.Size.Total > maxSize {
			maxSize = c.Size.Total
		}
	}

	rows := make([]string, 0, end-f.offset)
	for i := f.offset; i < end; i++ {
		c := children[i]
		row := renderRow(c, maxSize, i == f.selected)
		if mark, ok := nodeMark(c); ok && m.marks[mark] {
			row = "[D] " + row
		}
		rows = append(rows, row)
	}
	return bodyStyle.Render(strings.Join(rows, "\n"))
}

func renderRow(n *group.Node, maxSize int64, selected bool) string {
	bar := bar20(n.Size.Total, maxSize)
	marker := " "
	if n.Size.Shared > 0 && n.Size.Total != n.Size.Exclusive {
		marker = "~"
	}
	if n.Size.Estimated {
		marker = "~"
	}
	line := fmt.Sprintf("%s %s %10s  %s",
		bar, marker, size.Format(n.Size.Total, size.IEC), fmt.Sprintf("%s [%d]", n.Name, len(n.Children)))
	if selected {
		return selStyle.Render("▶ " + line)
	}
	return "  " + line
}

func bar20(v, max int64) string {
	const width = 20
	if max <= 0 {
		return strings.Repeat("░", width)
	}
	filled := int(float64(v) / float64(max) * width)
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func loadingView(start time.Time) string {
	return headerStyle.Render(fmt.Sprintf("dodu — scanning Docker daemon… (%s)", time.Since(start).Truncate(time.Millisecond)))
}

func helpView() string {
	lines := []string{
		"dodu — keys",
		"",
		"  j / down      move down",
		"  k / up        move up",
		"  l / enter     open child",
		"  h / esc       back",
		"  g             toggle layout (by-type ↔ by-project)",
		"  s             cycle sort (size → name → count)",
		"  r             rescan",
		"  Tab           toggle details",
		"  d / space     toggle object deletion mark",
		"  p             preview marked cleanup (dry-run)",
		"  e             export snapshot JSON to a new file",
		"  ?             toggle this help",
		"  q             quit",
	}
	return helpStyle.Render(strings.Join(lines, "\n"))
}

// SetReadOnly prevents interactive cleanup execution.
func (m *Model) SetReadOnly(v bool) { m.readOnly = v }

// SetInitialScan supplies an optional cached first scan. Refresh always bypasses it.
func (m *Model) SetInitialScan(fn func(context.Context) (*scan.Snapshot, error)) { m.initialScan = fn }

// SetContext binds scan and cleanup commands to the caller lifecycle.
func (m *Model) SetContext(ctx context.Context) { m.ctx = ctx }

func (m *Model) scanContext() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}
