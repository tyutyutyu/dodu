package export

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/scan"
	"github.com/tyutyutyu/dodu/pkg/size"
)

// Document is the top-level JSON shape.
type Document struct {
	SchemaVersion string            `json:"schema_version"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Tool          ToolInfo          `json:"tool"`
	Daemon        docker.DaemonInfo `json:"daemon"`
	Snapshot      SnapshotJSON      `json:"snapshot"`
	Totals        size.Totals       `json:"totals"`
}

// ToolInfo identifies the producer of the document.
type ToolInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SnapshotJSON is the JSON projection of scan.Snapshot.
type SnapshotJSON struct {
	LayersSize      int64                    `json:"layers_size"`
	LayersSizeKnown bool                     `json:"layers_size_known"`
	CapturedAt      time.Time                `json:"captured_at"`
	DurationMS      int64                    `json:"duration_ms"`
	Images          []docker.Image           `json:"images"`
	Containers      []docker.Container       `json:"containers"`
	Volumes         []docker.Volume          `json:"volumes"`
	BuildCache      []docker.BuildCacheEntry `json:"build_cache"`
	LogSizes        map[string]int64         `json:"log_sizes"`
	Errors          []string                 `json:"errors,omitempty"`
}

// ToJSON writes a Document to w.
func ToJSON(w io.Writer, snap *scan.Snapshot, tool ToolInfo) error {
	if snap == nil {
		return fmt.Errorf("export: nil snapshot")
	}
	doc := Document{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Tool:          tool,
		Daemon:        snap.Daemon,
		Snapshot: SnapshotJSON{
			LayersSize:      snap.LayersSize,
			LayersSizeKnown: snap.LayersSizeKnown,
			CapturedAt:      snap.CapturedAt.UTC(),
			DurationMS:      snap.Duration.Milliseconds(),
			Images:          snap.Images,
			Containers:      snap.Containers,
			Volumes:         snap.Volumes,
			BuildCache:      snap.BuildCache,
			LogSizes:        snap.LogSizes,
		},
		Totals: size.ComputeTotals(snap),
	}
	for _, e := range snap.Errors {
		doc.Snapshot.Errors = append(doc.Snapshot.Errors, e.Error())
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
