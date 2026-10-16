package scan

import (
	"time"

	"github.com/tyutyutyu/dodu/pkg/docker"
)

// Snapshot is a point-in-time capture of Docker disk usage data.
//
// It is the canonical input for sizing, grouping, planning, caching, and
// export. Field types come from pkg/docker so consumers never need to import
// the SDK.
type Snapshot struct {
	// LayersSize is the daemon aggregate image-layer usage, including shared layers once.
	LayersSize      int64
	LayersSizeKnown bool

	Daemon     docker.DaemonInfo
	Images     []docker.Image
	Containers []docker.Container
	Volumes    []docker.Volume
	BuildCache []docker.BuildCacheEntry

	// LogSizes maps container ID → JSON log file size in bytes.
	// Missing entries should be treated as zero.
	LogSizes map[string]int64

	CapturedAt time.Time
	Duration   time.Duration

	// Errors holds non-fatal collector errors. A non-empty slice means the
	// snapshot is still usable but partial.
	Errors []error
}

// LogSize returns the recorded log file size for a container, or zero.
func (s *Snapshot) LogSize(containerID string) int64 {
	if s == nil || s.LogSizes == nil {
		return 0
	}
	return s.LogSizes[containerID]
}
