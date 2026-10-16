package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/group"
	"github.com/tyutyutyu/dodu/pkg/plan"
	"github.com/tyutyutyu/dodu/pkg/scan"
)

func fixtureModel() *Model {
	m := NewModel(nil, nil)
	m.loading = false
	m.width, m.height = 80, 24
	m.snap = &scan.Snapshot{Images: []docker.Image{{ID: "image-1", RepoTags: []string{"example:latest"}, Size: 100, SharedSize: 0}}, Containers: []docker.Container{{ID: "container-1", ImageID: "image-1", State: "running", Names: []string{"example"}}}}
	m.rebuildTree()
	return m
}

func key(m *Model, s string) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return cmd
}

func TestGroupingAndSortPreserveObjectSelection(t *testing.T) {
	m := fixtureModel()
	m.restoreSelection(&group.Node{Kind: group.KindImage, Meta: map[string]string{"id": "image-1"}})
	for _, k := range []string{"s", "g", "s", "g"} {
		key(m, k)
		selected := m.selectedNode()
		if selected == nil || selected.Meta["id"] != "image-1" {
			t.Fatalf("selection lost after %s: %+v", k, selected)
		}
	}
}

func TestBuildCacheMarksSurviveLayoutChanges(t *testing.T) {
	m := fixtureModel()
	m.snap = &scan.Snapshot{BuildCache: []docker.BuildCacheEntry{{ID: "cache-1", Size: 100}}}
	m.rebuildTree()
	m.restoreSelection(&group.Node{Kind: group.KindBuildCache, Meta: map[string]string{"id": "cache-1"}})
	key(m, "d")
	key(m, "g")
	selected := m.selectedNode()
	if selected == nil || selected.Meta["id"] != "cache-1" {
		t.Fatalf("cache selection lost in project layout: %+v", selected)
	}
	key(m, "p")
	if len(m.preview.Items) != 1 || m.preview.Items[0].ID != "cache-1" {
		t.Fatalf("cache mark lost in project layout: %+v", m.preview)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	key(m, "d")
	if len(m.marks) != 0 {
		t.Fatal("cannot unmark cache in project layout")
	}
	key(m, "d")
	key(m, "g")
	key(m, "p")
	if len(m.preview.Items) != 1 || m.preview.Items[0].ID != "cache-1" {
		t.Fatalf("project-layout cache mark lost in type layout: %+v", m.preview)
	}
}

func TestPlanBlocksReferencedImageAndRequiresExactConfirmation(t *testing.T) {
	m := fixtureModel()
	m.restoreSelection(&group.Node{Kind: group.KindImage, Meta: map[string]string{"id": "image-1"}})
	key(m, "d")
	key(m, "p")
	if len(m.preview.Blocked) != 1 || len(m.preview.Items) != 0 {
		t.Fatalf("unguarded plan: %+v", m.preview)
	}
	if cmd := key(m, "x"); cmd != nil {
		t.Fatal("blocked plan started execution")
	}
	m.preview = &plan.Plan{Items: []plan.Item{{Kind: group.KindImage, ID: "safe"}}}
	m.confirming = true
	key(m, "ye")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.executing {
		t.Fatal("incomplete confirmation executed")
	}
	key(m, "s")
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.executing {
		t.Fatal("exact yes should schedule guarded execution")
	}
}

func TestReadonlyCannotPrepareExecution(t *testing.T) {
	m := fixtureModel()
	m.SetReadOnly(true)
	m.preview = &plan.Plan{Items: []plan.Item{{ID: "safe"}}}
	if cmd := key(m, "x"); cmd != nil || m.preparing {
		t.Fatal("readonly prepared cleanup")
	}
}

func TestRenderingFitsTerminalAndDetailsAreSnapshotOnly(t *testing.T) {
	m := fixtureModel()
	m.restoreSelection(&group.Node{Kind: group.KindImage, Meta: map[string]string{"id": "image-1"}})
	if !strings.Contains(m.detailsView(), "example:latest") {
		t.Fatal("missing image details")
	}
	for _, width := range []int{40, 80, 120} {
		m.width = width
		view := m.View()
		if lipgloss.Width(view) > width || lipgloss.Height(view) > 24 {
			t.Fatalf("overflow %dx%d: %dx%d", width, 24, lipgloss.Width(view), lipgloss.Height(view))
		}
	}
}

func TestInitialLoaderUsedOnce(t *testing.T) {
	m := fixtureModel()
	calls := 0
	m.SetInitialScan(func(context.Context) (*scan.Snapshot, error) { calls++; return m.snap, nil })
	if _, ok := m.Init()().(scanDoneMsg); !ok || calls != 1 {
		t.Fatal("initial loader not used")
	}
}

func TestBarHandlesLargeValues(t *testing.T) {
	bar := bar20(1<<62, 1<<62)
	if bar != strings.Repeat("█", 20) {
		t.Fatalf("overflow: %s", bar)
	}
}

func TestHelpCannotPrepareHiddenCleanup(t *testing.T) {
	m := fixtureModel()
	m.restoreSelection(&group.Node{Kind: group.KindImage, Meta: map[string]string{"id": "image-1"}})
	key(m, "?")
	for _, k := range []string{"d", "p", "x", "yes"} {
		if key(m, k) != nil {
			t.Fatalf("help dispatched command %s", k)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.marks) > 0 || m.preview != nil || m.preparing || m.executing {
		t.Fatal("help allowed invisible planner actions")
	}
}

func TestHiddenPlannerCannotAcceptCleanupKeys(t *testing.T) {
	for _, screen := range []string{"loading", "error"} {
		for _, confirming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/confirming=%t", screen, confirming), func(t *testing.T) {
				m := fixtureModel()
				m.preview = &plan.Plan{Items: []plan.Item{{Kind: group.KindImage, ID: "safe"}}}
				m.confirming = confirming
				m.loading = screen == "loading"
				if screen == "error" {
					m.err = errors.New("temporary scan failure")
				}
				for _, k := range []string{"d", "p", "x", "yes"} {
					if key(m, k) != nil {
						t.Fatalf("hidden planner dispatched %s", k)
					}
				}
				_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				if cmd != nil || m.preparing || m.executing || m.confirmation != "" || len(m.marks) != 0 {
					t.Fatal("hidden planner accepted cleanup input")
				}
			})
		}
	}
}

func TestFailedScanRequiresSuccessfulRetryBeforeCleanup(t *testing.T) {
	m := fixtureModel()
	m.snap = &scan.Snapshot{Containers: []docker.Container{{ID: "stopped", State: "exited"}}}
	m.rebuildTree()
	m.restoreSelection(&group.Node{Kind: group.KindContainer, Meta: map[string]string{"id": "stopped"}})
	key(m, "d")
	key(m, "r")
	m.Update(scanDoneMsg{err: errors.New("temporary scan failure")})
	key(m, "p")
	if m.preview != nil {
		t.Fatal("error screen opened a hidden plan")
	}
	if key(m, "r") == nil {
		t.Fatal("error screen cannot retry scan")
	}
	if key(m, "r") != nil {
		t.Fatal("loading screen started another scan")
	}
	m.Update(scanDoneMsg{snap: m.snap})
	key(m, "p")
	if key(m, "x") == nil {
		t.Fatal("successful retry cannot prepare cleanup")
	}
	defer m.operationCancel()
	m.Update(prepareDoneMsg{snap: m.snap})
	if !strings.Contains(m.View(), "Type yes") {
		t.Fatal("cleanup confirmation is hidden after recovery")
	}
	key(m, "yes")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.executing {
		t.Fatal("visible confirmation cannot execute cleanup")
	}
	m.operationCancel()
}

func TestInterruptWaitsForOperationBeforeQuit(t *testing.T) {
	for _, executing := range []bool{false, true} {
		m := fixtureModel()
		ctx, cancel := context.WithCancel(context.Background())
		m.operationCancel = cancel
		m.executing = executing
		m.preparing = !executing
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if cmd != nil || ctx.Err() != context.Canceled || !m.quitAfterOperation {
			t.Fatal("interrupt did not cancel and wait")
		}
		var done tea.Msg = prepareDoneMsg{err: context.Canceled}
		if executing {
			done = executeDoneMsg{err: context.Canceled}
		}
		_, cmd = m.Update(done)
		if cmd == nil {
			t.Fatal("operation completion did not quit")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("expected quit after operation completion")
		}
	}
}

func TestInterruptExitsConfirmation(t *testing.T) {
	m := fixtureModel()
	m.confirming = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("confirmation swallowed interrupt")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("confirmation did not quit")
	}
}
