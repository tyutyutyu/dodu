package scan_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/docker/mock"
	"github.com/tyutyutyu/dodu/pkg/scan"
)

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestScanHappyPath(t *testing.T) {
	m := mock.New()
	m.Daemon = docker.DaemonInfo{ID: "d-1", ServerVersion: "26.0"}
	m.Images = []docker.Image{{ID: "img1", Size: 100}}
	m.Containers = []docker.Container{{ID: "c1", SizeRw: 5, ImageID: "img1"}}
	m.Volumes = []docker.Volume{{Name: "v1", UsageBytes: 200}}
	m.BuildCache = []docker.BuildCacheEntry{{ID: "bc1", Size: 10}}
	m.LogSizes = map[string]int64{"c1": 4096}

	s := scan.New(m, newSilentLogger())
	snap, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if snap.Daemon.ID != "d-1" {
		t.Errorf("daemon id = %q", snap.Daemon.ID)
	}
	if len(snap.Images) != 1 || len(snap.Containers) != 1 || len(snap.Volumes) != 1 || len(snap.BuildCache) != 1 {
		t.Errorf("unexpected counts: %+v", snap)
	}
	if got := snap.LogSize("c1"); got != 4096 {
		t.Errorf("log size c1 = %d, want 4096", got)
	}
	if len(snap.Errors) != 0 {
		t.Errorf("unexpected errors: %v", snap.Errors)
	}
	if snap.Duration <= 0 {
		t.Errorf("expected positive duration, got %v", snap.Duration)
	}
}

func TestScanPartialFailure(t *testing.T) {
	m := mock.New()
	m.DiskUsageFunc = func(context.Context) (docker.DiskUsage, error) {
		return docker.DiskUsage{}, errors.New("disk usage failed")
	}
	m.Daemon = docker.DaemonInfo{ID: "d-1"}
	m.Images = []docker.Image{{ID: "img1"}}
	m.ListVolumesFunc = func(_ context.Context) ([]docker.Volume, error) {
		return nil, errors.New("volumes boom")
	}
	m.BuildCacheFunc = func(_ context.Context) ([]docker.BuildCacheEntry, error) {
		return nil, errors.New("bc boom")
	}

	s := scan.New(m, newSilentLogger())
	snap, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan should not abort on partial failure: %v", err)
	}
	if len(snap.Images) != 1 {
		t.Errorf("images should still be collected, got %d", len(snap.Images))
	}
	if len(snap.Errors) < 2 {
		t.Errorf("expected >=2 errors, got %d", len(snap.Errors))
	}
}

func TestScanPingFailureAborts(t *testing.T) {
	m := mock.New()
	m.PingFunc = func(_ context.Context) (docker.DaemonInfo, error) {
		return docker.DaemonInfo{}, errors.New("no daemon")
	}
	s := scan.New(m, newSilentLogger())
	if _, err := s.Scan(context.Background()); err == nil {
		t.Fatal("expected error when ping fails")
	}
}

func TestScanContextCancel(t *testing.T) {
	m := mock.New()
	m.DiskUsageFunc = func(ctx context.Context) (docker.DiskUsage, error) {
		<-ctx.Done()
		return docker.DiskUsage{}, ctx.Err()
	}
	s := scan.New(m, newSilentLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	snap, err := s.Scan(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || snap != nil {
		t.Fatalf("cancelled scan = %v, %v; want nil, deadline exceeded", snap, err)
	}
}

func BenchmarkScan(b *testing.B) {
	m := mock.New()
	m.Daemon = docker.DaemonInfo{ID: "d"}
	for i := 0; i < 200; i++ {
		m.Images = append(m.Images, docker.Image{ID: "img", Size: 100})
	}
	for i := 0; i < 50; i++ {
		m.Containers = append(m.Containers, docker.Container{ID: "c"})
	}
	for i := 0; i < 100; i++ {
		m.Volumes = append(m.Volumes, docker.Volume{Name: "v"})
	}
	s := scan.New(m, newSilentLogger())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Scan(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func TestScanCancellationWaitsForLogWorkers(t *testing.T) {
	m := mock.New()
	m.Containers = []docker.Container{{ID: "one"}, {ID: "two"}, {ID: "three"}}
	var active atomic.Int32
	started := make(chan struct{})
	m.LogFileSizeFunc = func(ctx context.Context, id string) (int64, error) {
		active.Add(1)
		defer active.Add(-1)
		if id == "one" {
			close(started)
		}
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		return 0, ctx.Err()
	}
	s := scan.New(m, newSilentLogger())
	s.LogConcurrency = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-started; cancel() }()
	snap, err := s.Scan(ctx)
	if !errors.Is(err, context.Canceled) || snap != nil {
		t.Fatalf("scan = %v, %v", snap, err)
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("scan returned with %d active log workers", got)
	}
}

func TestScanUsesSingleDaemonUsageSnapshot(t *testing.T) {
	m := mock.New()
	m.DiskUsageFunc = func(context.Context) (docker.DiskUsage, error) {
		return docker.DiskUsage{LayersSize: 400, Images: []docker.Image{{ID: "image", Size: 400, SharedSize: 300}}, Containers: []docker.Container{{ID: "container"}}, Volumes: []docker.Volume{{Name: "volume", UsageBytes: 700}}}, nil
	}
	snap, err := scan.New(m, newSilentLogger()).Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LayersSizeKnown || snap.LayersSize != 400 || len(snap.Images) != 1 || len(snap.Containers) != 1 || len(snap.Volumes) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if m.CallCount["DiskUsage"] != 1 || m.CallCount["ListImages"] != 0 || m.CallCount["ListVolumes"] != 0 || m.CallCount["BuildCacheUsage"] != 0 {
		t.Fatalf("calls = %v", m.CallCount)
	}
}
