package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tyutyutyu/dodu/pkg/group"
	"github.com/tyutyutyu/dodu/pkg/plan"
	"github.com/tyutyutyu/dodu/pkg/scan"
	"github.com/tyutyutyu/dodu/pkg/size"
)

func newPruneCmd(root *rootFlags) *cobra.Command {
	var (
		apply bool
		yes   bool
		kinds []string
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Plan or execute reclamation of unused Docker disk usage",
		Long: "Build a guarded prune plan from the current snapshot. Default is dry-run.\n" +
			"Pass --apply --yes to execute non-interactively (CI mode).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, kind := range kinds {
				switch kind {
				case "image", "container", "volume", "build_cache":
				default:
					return fmt.Errorf("unknown kind %q", kind)
				}
			}
			if apply {
				if err := abortIfReadOnly(root); err != nil {
					return err
				}
				if !yes {
					return fmt.Errorf("--apply requires --yes")
				}
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

			fresh := *root
			fresh.noCache = true
			snap, err := loadOrScan(ctx, client, &fresh)
			if err != nil {
				return err
			}
			if len(snap.Errors) > 0 {
				return fmt.Errorf("cannot plan cleanup from an incomplete scan: %v", snap.Errors)
			}
			marks := defaultMarks(snap, kinds)
			pl := plan.Build(snap, marks)
			renderPlan(cmd.OutOrStdout(), pl)

			if !apply {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\n(dry-run) re-run with --apply --yes to execute.")
				return nil
			}
			if err := abortIfReadOnly(root); err != nil {
				return err
			}
			if !yes {
				if isTTY(os.Stdin) {
					return fmt.Errorf("interactive --apply not supported in CLI; pass --yes")
				}
				return fmt.Errorf("--apply requires --yes when stdin is not a TTY")
			}
			audit, err := plan.DefaultAuditLogPath()
			if err != nil {
				return fmt.Errorf("resolve audit path: %w", err)
			}
			rep, err := pl.Execute(ctx, client, plan.ExecOptions{AuditLog: audit})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nExecuted %d items, reclaimed ~%s\n",
				len(rep.Results), size.Format(rep.ActualReclaim, size.IEC))
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "actually execute the plan (otherwise dry-run)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip interactive confirmation (required with --apply)")
	cmd.Flags().StringSliceVar(&kinds, "kind", []string{"image", "container", "volume", "build_cache"},
		"object kinds to consider: image,container,volume,build_cache")
	return cmd
}

// defaultMarks selects all reclaimable candidates (dangling/stopped/unused).
func defaultMarks(snap *scan.Snapshot, kinds []string) []plan.Mark {
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	var marks []plan.Mark
	if want["image"] {
		for _, im := range snap.Images {
			if len(im.RepoTags) == 0 || (len(im.RepoTags) == 1 && im.RepoTags[0] == "<none>:<none>") {
				marks = append(marks, plan.Mark{Kind: group.KindImage, ID: im.ID})
			}
		}
	}
	if want["container"] {
		for _, c := range snap.Containers {
			if c.State == "exited" || c.State == "created" || c.State == "dead" {
				marks = append(marks, plan.Mark{Kind: group.KindContainer, ID: c.ID})
			}
		}
	}
	if want["volume"] {
		for _, v := range snap.Volumes {
			if v.RefCount == 0 {
				marks = append(marks, plan.Mark{Kind: group.KindVolume, ID: v.Name})
			}
		}
	}
	if want["build_cache"] {
		for _, b := range snap.BuildCache {
			if !b.InUse {
				marks = append(marks, plan.Mark{Kind: group.KindBuildCache, ID: b.ID})
			}
		}
	}
	return marks
}

func renderPlan(w io.Writer, p *plan.Plan) {
	_, _ = fmt.Fprintf(w, "Plan: %d items, ~%s reclaimable\n", len(p.Items), size.Format(p.EstReclaim, size.IEC))
	for _, it := range p.Items {
		_, _ = fmt.Fprintf(w, "  + [%s] %-40s ~%s  %s\n", it.Kind, truncate(it.Name, 40), size.Format(it.EstReclaim, size.IEC), it.Reason)
	}
	if len(p.Blocked) > 0 {
		_, _ = fmt.Fprintf(w, "\nBlocked: %d items\n", len(p.Blocked))
		for _, it := range p.Blocked {
			_, _ = fmt.Fprintf(w, "  ! [%s] %-40s  %s\n", it.Kind, truncate(it.Name, 40), it.Reason)
		}
	}
	if len(p.Warnings) > 0 {
		_, _ = fmt.Fprintln(w, "\nWarnings:")
		for _, w2 := range p.Warnings {
			_, _ = fmt.Fprintf(w, "  ~ %s\n", w2)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func isTTY(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
