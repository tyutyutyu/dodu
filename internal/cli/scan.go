package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tyutyutyu/dodu/pkg/export"
	"github.com/tyutyutyu/dodu/pkg/scan"
	"github.com/tyutyutyu/dodu/pkg/size"
)

func newScanCmd(info BuildInfo, root *rootFlags) *cobra.Command {
	var format string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the daemon and print disk usage",
		Long:  "Scan the local Docker daemon and print disk usage in text, JSON, or CSV.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonOutput {
				format = "json"
			}
			switch format {
			case "text", "json", "csv-images", "csv-containers", "csv-volumes", "csv-build_cache":
			default:
				return fmt.Errorf("unknown format %q", format)
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
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
			return renderScan(cmd.OutOrStdout(), snap, format, info)
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json|csv-images|csv-containers|csv-volumes|csv-build_cache")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output JSON (alias for --format json)")
	return cmd
}

func renderScan(w io.Writer, snap *scan.Snapshot, format string, info BuildInfo) error {
	switch format {
	case "json":
		return export.ToJSON(w, snap, export.ToolInfo{Name: "dodu", Version: info.Version})
	case "csv-images":
		return export.ToCSV(w, snap, export.CSVImages)
	case "csv-containers":
		return export.ToCSV(w, snap, export.CSVContainers)
	case "csv-volumes":
		return export.ToCSV(w, snap, export.CSVVolumes)
	case "csv-build_cache":
		return export.ToCSV(w, snap, export.CSVBuildCache)
	case "text", "":
		return renderText(w, snap)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}

func renderText(w io.Writer, snap *scan.Snapshot) error {
	tot := size.ComputeTotals(snap)
	var writeErr error
	fp := func(format string, a ...any) {
		if writeErr == nil {
			_, writeErr = fmt.Fprintf(w, format, a...)
		}
	}
	fp("Daemon:      %s (%s)\n", snap.Daemon.ID, snap.Daemon.ServerVersion)
	fp("Captured:    %s (%s)\n", snap.CapturedAt.Local().Format("2006-01-02 15:04:05"), snap.Duration)
	fp("\n")
	fp("  Images       %10s  (%d)\n", size.Format(tot.Images, size.IEC), len(snap.Images))
	fp("  Containers   %10s  (%d)\n", size.Format(tot.Containers, size.IEC), len(snap.Containers))
	fp("  Volumes      %10s  (%d)\n", size.Format(tot.Volumes, size.IEC), len(snap.Volumes))
	fp("  Build cache  %10s  (%d)\n", size.Format(tot.BuildCache, size.IEC), len(snap.BuildCache))
	fp("  Logs         %10s (readable local log files only)\n", size.Format(tot.Logs, size.IEC))
	fp("\n")
	fp("  Reclaimable  %10s\n", size.Format(tot.Reclaimable, size.IEC))
	if len(snap.Errors) > 0 {
		fp("\nPartial failures (%d):\n", len(snap.Errors))
		for _, e := range snap.Errors {
			fp("  - %v\n", e)
		}
	}
	return writeErr
}
