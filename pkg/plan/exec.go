package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/group"
)

// ErrReadOnly is returned when DODU_READONLY=1 blocks an execute.
var ErrReadOnly = errors.New("plan: execution disabled (DODU_READONLY=1 or readonly option)")

// Report is the outcome of executing a Plan.
type Report struct {
	Results       []ItemResult
	ActualReclaim int64
	StartedAt     time.Time
	FinishedAt    time.Time
}

// ItemResult records what happened to a single planned item.
type ItemResult struct {
	Item    Item
	Success bool
	Err     string
	Reclaim int64
}

// ExecOptions tunes Execute.
type ExecOptions struct {
	// ReadOnly aborts immediately. Defaults to true if env DODU_READONLY=1.
	ReadOnly bool
	// AuditLog is opened in append mode for JSON-Lines records. Empty disables.
	AuditLog string
	// Force is rejected: a guarded plan must never kill active containers.
	Force bool
}

// Execute carries out the Plan via the docker.Client. Blocked items are never
// touched. All actions are appended to the audit log when configured.
func (p *Plan) Execute(ctx context.Context, client docker.Client, opts ExecOptions) (rep Report, retErr error) {
	if p == nil {
		return Report{}, errors.New("plan: nil")
	}
	if opts.ReadOnly || os.Getenv("DODU_READONLY") == "1" {
		return Report{}, ErrReadOnly
	}
	if opts.Force {
		return Report{}, errors.New("plan: forced removal is incompatible with guarded execution")
	}
	rep = Report{StartedAt: time.Now()}
	defer func() { rep.FinishedAt = time.Now() }()

	var audit *os.File
	if opts.AuditLog != "" {
		if err := os.MkdirAll(filepath.Dir(opts.AuditLog), 0o750); err != nil {
			return rep, fmt.Errorf("create audit directory: %w", err)
		}
		f, err := os.OpenFile(opts.AuditLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return rep, fmt.Errorf("open audit log: %w", err)
		}
		audit = f
		defer func() { retErr = errors.Join(retErr, f.Close()) }()
	}
	writeAudit := func(rec map[string]any) error {
		if audit == nil {
			return nil
		}
		if err := json.NewEncoder(audit).Encode(rec); err != nil {
			return err
		}
		return audit.Sync()
	}
	var failures []error

	for _, it := range p.Items {
		select {
		case <-ctx.Done():
			return rep, errors.Join(append(failures, ctx.Err())...)
		default:
		}
		// Persist intent before touching Docker so audit failures stop safely.
		if err := writeAudit(map[string]any{
			"ts":       time.Now().UTC().Format(time.RFC3339Nano),
			"phase":    "intent",
			"kind":     string(it.Kind),
			"id":       it.ID,
			"name":     it.Name,
			"estimate": it.EstReclaim,
		}); err != nil {
			return rep, errors.Join(append(failures, fmt.Errorf("write audit intent: %w", err))...)
		}
		res := ItemResult{Item: it}
		var err error
		switch it.Kind {
		case group.KindImage:
			_, err = client.RemoveImage(ctx, it.ID, opts.Force, false)
		case group.KindContainer:
			err = client.RemoveContainer(ctx, it.ID, opts.Force, false)
		case group.KindVolume:
			err = client.RemoveVolume(ctx, it.ID, opts.Force)
		case group.KindBuildCache:
			// Limit prune to the selected entry; never prune unrelated cache.
			report, perr := client.PruneBuildCache(ctx, docker.PruneFilters{IDs: []string{it.ID}})
			err = perr
			if err == nil && !slices.Contains(report.Deleted, it.ID) {
				err = fmt.Errorf("daemon did not report selected build cache %s as deleted", it.ID)
			}
			if err == nil {
				res.Reclaim = report.SpaceReclaimed
			}
		default:
			err = fmt.Errorf("unsupported kind %s", it.Kind)
		}
		if err != nil {
			res.Err = err.Error()
			failures = append(failures, fmt.Errorf("remove %s %s: %w", it.Kind, it.ID, err))
		} else {
			res.Success = true
			if res.Reclaim == 0 && it.Kind != group.KindBuildCache {
				res.Reclaim = it.EstReclaim
			}
			rep.ActualReclaim += res.Reclaim
		}
		rep.Results = append(rep.Results, res)
		if err := writeAudit(map[string]any{
			"ts":       time.Now().UTC().Format(time.RFC3339Nano),
			"kind":     string(it.Kind),
			"id":       it.ID,
			"name":     it.Name,
			"phase":    "result",
			"success":  res.Success,
			"err":      res.Err,
			"reclaim":  res.Reclaim,
			"estimate": it.EstReclaim,
		}); err != nil {
			return rep, errors.Join(append(failures, fmt.Errorf("write audit log: %w", err))...)
		}
	}

	return rep, errors.Join(failures...)
}

// DefaultAuditLogPath returns $XDG_STATE_HOME/dodu/audit.log or
// $HOME/.local/state/dodu/audit.log.
func DefaultAuditLogPath() (string, error) {
	if p := os.Getenv("XDG_STATE_HOME"); p != "" {
		return filepath.Join(p, "dodu", "audit.log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "dodu", "audit.log"), nil
}
