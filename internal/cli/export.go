package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tyutyutyu/dodu/pkg/export"
)

func newExportCmd(info BuildInfo, root *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <file>",
		Short: "Export a snapshot to JSON or CSV",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			path := args[0]
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".json" && ext != ".csv" {
				return fmt.Errorf("unsupported extension %q (use .json or .csv)", ext)
			}
			client, err := newClient(root)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			snap, err := loadOrScan(ctx, client, root)
			if err != nil {
				return err
			}

			f, err := os.Create(path) //nolint:gosec // user-supplied path
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()

			switch ext {
			case ".json":
				return export.ToJSON(f, snap, export.ToolInfo{Name: "dodu", Version: info.Version})
			case ".csv":
				return export.ToCSV(f, snap, export.CSVImages)
			default:
				return fmt.Errorf("unsupported extension %q (use .json or .csv)", ext)
			}
		},
	}
	return cmd
}
