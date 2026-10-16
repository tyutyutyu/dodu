package cli

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/tyutyutyu/dodu/internal/tui"
	"github.com/tyutyutyu/dodu/pkg/scan"
)

// runTUI launches the Bubbletea TUI bound to the Docker client created from
// the persistent flags. Declared as a var so tests can stub it out.
var runTUI = func(cmd *cobra.Command, f *rootFlags) error {
	client, err := newClient(f)
	if err != nil {
		return fmt.Errorf("connect to docker: %w", err)
	}
	defer func() { _ = client.Close() }()

	model := tui.NewModel(client, nil)
	model.SetReadOnly(abortIfReadOnly(f) != nil)
	model.SetContext(cmd.Context())
	model.SetInitialScan(func(ctx context.Context) (*scan.Snapshot, error) { return loadOrScan(ctx, client, f) })
	prog := tea.NewProgram(model, tea.WithContext(cmd.Context()), tea.WithAltScreen())
	_, err = prog.Run()
	return err
}
