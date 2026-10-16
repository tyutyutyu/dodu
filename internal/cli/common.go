package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tyutyutyu/dodu/pkg/cache"
	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/plan"
	"github.com/tyutyutyu/dodu/pkg/scan"
)

// Exit codes returned by Execute.
const (
	ExitOK             = 0
	ExitGeneral        = 1
	ExitDaemonUnreach  = 2
	ExitReadOnlyDenied = 3
)

// rootFlags collects persistent flags from the root command.
type rootFlags struct {
	host     string
	noCache  bool
	logLevel string
	readonly bool
}

func attachPersistentFlags(root *cobra.Command, f *rootFlags) {
	root.PersistentFlags().StringVar(&f.host, "host", "", "Docker daemon host (default: env DOCKER_HOST or socket)")
	root.PersistentFlags().BoolVar(&f.noCache, "no-cache", false, "ignore cached snapshot")
	root.PersistentFlags().StringVar(&f.logLevel, "log-level", "warn", "log level: debug|info|warn|error")
	root.PersistentFlags().BoolVar(&f.readonly, "readonly", false, "deny any destructive action (also: DODU_READONLY=1)")
}

// newClient constructs a docker.Client honouring rootFlags.
func newClient(f *rootFlags) (docker.Client, error) {
	var opts []docker.Option
	if f.host != "" {
		opts = append(opts, docker.WithHost(f.host))
	}
	c, err := docker.New(opts...)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// loadOrScan returns a snapshot, preferring cache if --no-cache is unset and
// the entry is fresh per the default policy.
func loadOrScan(ctx context.Context, client docker.Client, f *rootFlags) (*scan.Snapshot, error) {
	if !f.noCache {
		if snap := cachedSnapshot(ctx, client); snap != nil {
			return snap, nil
		}
	}

	scanner := scan.New(client, nil)
	snap, err := scanner.Scan(ctx)
	if err != nil {
		return nil, err
	}
	if !f.noCache {
		if path, perr := cache.DefaultPath(); perr == nil {
			if c, perr := cache.OpenBolt(path); perr == nil {
				_ = c.Save(cache.Key(snap.Daemon), snap)
				_ = c.Close()
			}
		}
	}
	return snap, nil
}

// cachedSnapshot releases the database before a cache miss starts a scan.
func cachedSnapshot(ctx context.Context, client docker.Client) *scan.Snapshot {
	path, err := cache.DefaultPath()
	if err != nil {
		return nil
	}
	c, err := cache.OpenBolt(path)
	if err != nil {
		return nil
	}
	defer func() { _ = c.Close() }()
	info, err := client.Ping(ctx)
	if err != nil {
		return nil
	}
	snap, err := c.Load(cache.Key(info))
	if err != nil || !cache.DefaultPolicy.Fresh(snap, time.Now()) {
		return nil
	}
	return snap
}

// classifyExit maps an error to a process exit code.
func classifyExit(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, docker.ErrDaemonUnreachable):
		return ExitDaemonUnreach
	case errors.Is(err, docker.ErrReadOnly), errors.Is(err, plan.ErrReadOnly):
		return ExitReadOnlyDenied
	default:
		return ExitGeneral
	}
}

// printErr writes an error message to stderr.
func printErr(cmd *cobra.Command, err error) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dodu: %v\n", err)
}

// abortIfReadOnly returns an error when readonly mode is active.
func abortIfReadOnly(f *rootFlags) error {
	if f.readonly || os.Getenv("DODU_READONLY") == "1" {
		return docker.ErrReadOnly
	}
	return nil
}
