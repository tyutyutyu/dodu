// Package cli wires up the dodu Cobra command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// BuildInfo holds version metadata injected at build time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Execute runs the root command and returns the process exit code.
func Execute(info BuildInfo) int {
	root := newRootCmd(info)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		printErr(root, err)
		return classifyExit(err)
	}
	return ExitOK
}

func newRootCmd(info BuildInfo) *cobra.Command {
	flags := &rootFlags{}
	root := &cobra.Command{
		Use:   "dodu",
		Short: "Docker disk atlas — ncdu-style TUI/CLI for Docker storage",
		Long: "dodu is a fast, navigable TUI/CLI that shows what consumes disk in your\n" +
			"Docker environment (images, containers, volumes, build cache, logs) and\n" +
			"suggests safe, dry-run cleanup plans.",
		Version: info.Version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			levels := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
			level, ok := levels[strings.ToLower(flags.logLevel)]
			if !ok {
				return fmt.Errorf("unknown log level %q", flags.logLevel)
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level})))
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd, flags)
		},
	}
	attachPersistentFlags(root, flags)

	root.AddCommand(
		newVersionCmd(info),
		newScanCmd(info, flags),
		newPruneCmd(flags),
		newExportCmd(info, flags),
		newCacheCmd(),
	)
	return root
}

func newVersionCmd(info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printVersion(cmd.OutOrStdout(), info)
		},
	}
}

func printVersion(w io.Writer, info BuildInfo) error {
	_, err := fmt.Fprintf(w, "dodu %s (commit %s, built %s)\n", info.Version, info.Commit, info.Date)
	return err
}
