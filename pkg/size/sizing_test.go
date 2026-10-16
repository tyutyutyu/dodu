package size_test

import (
	"testing"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/scan"
	"github.com/tyutyutyu/dodu/pkg/size"
)

func TestImageSizes(t *testing.T) {
	cases := []struct {
		name string
		in   docker.Image
		want size.ImageSize
	}{
		{
			name: "shared known",
			in:   docker.Image{ID: "a", Size: 1000, SharedSize: 300},
			want: size.ImageSize{Total: 1000, Shared: 300, Exclusive: 700},
		},
		{
			name: "shared unknown",
			in:   docker.Image{ID: "b", Size: 500, SharedSize: -1},
			want: size.ImageSize{Total: 500, Shared: 0, Exclusive: 500, Estimated: true},
		},
		{
			name: "shared exceeds size",
			in:   docker.Image{ID: "c", Size: 100, SharedSize: 200},
			want: size.ImageSize{Total: 100, Shared: 200, Exclusive: 0},
		},
		{
			name: "zero",
			in:   docker.Image{ID: "z"},
			want: size.ImageSize{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := size.ImageSizes([]docker.Image{tc.in})
			got := out[tc.in.ID]
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestContainerSizes(t *testing.T) {
	cs := []docker.Container{
		{ID: "c1", SizeRw: 100},
		{ID: "c2", SizeRw: -1}, // negative → 0
	}
	logs := map[string]int64{"c1": 50, "c2": 30, "missing": 999}
	got := size.ContainerSizes(cs, logs)
	if got["c1"].Total != 150 {
		t.Errorf("c1 total = %d", got["c1"].Total)
	}
	if got["c2"].WritableLayer != 0 || got["c2"].Total != 30 {
		t.Errorf("c2 = %+v", got["c2"])
	}
	if _, ok := got["missing"]; ok {
		t.Errorf("missing container should not appear in result")
	}
}

func TestComputeTotals(t *testing.T) {
	snap := &scan.Snapshot{
		Images: []docker.Image{
			{ID: "img1", RepoTags: []string{"foo:1"}, Size: 1000, SharedSize: 200},
			{ID: "dangling", RepoTags: nil, Size: 500, SharedSize: 0},
		},
		Containers: []docker.Container{
			{ID: "c1", State: "running", SizeRw: 50},
			{ID: "c2", State: "exited", SizeRw: 30},
		},
		Volumes: []docker.Volume{
			{Name: "v1", UsageBytes: 700, RefCount: 2},
			{Name: "v2", UsageBytes: 100, RefCount: 0},
			{Name: "v3", UsageBytes: -1}, // unknown → skipped
		},
		BuildCache: []docker.BuildCacheEntry{
			{ID: "b1", Size: 40, InUse: true},
			{ID: "b2", Size: 60, InUse: false},
		},
		LogSizes: map[string]int64{"c1": 10, "c2": 20},
	}
	tot := size.ComputeTotals(snap)

	// images: exclusive sums = 800 + 500 = 1300
	if tot.Images != 1300 {
		t.Errorf("Images = %d, want 1300", tot.Images)
	}
	if tot.Containers != 80 {
		t.Errorf("Containers = %d, want 80", tot.Containers)
	}
	if tot.Logs != 30 {
		t.Errorf("Logs = %d, want 30", tot.Logs)
	}
	if tot.Volumes != 800 {
		t.Errorf("Volumes = %d, want 800", tot.Volumes)
	}
	if tot.BuildCache != 100 {
		t.Errorf("BuildCache = %d, want 100", tot.BuildCache)
	}
	// reclaim: dangling image (500) + exited container (30+20) + unused volume (100) + unused bc (60)
	if tot.Reclaimable != 500+50+100+60 {
		t.Errorf("Reclaimable = %d, want %d", tot.Reclaimable, 500+50+100+60)
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		bytes int64
		unit  size.Unit
		want  string
	}{
		{0, size.IEC, "0 B"},
		{512, size.IEC, "512 B"},
		{1024, size.IEC, "1.0 KiB"},
		{1536, size.IEC, "1.5 KiB"},
		{1000, size.SI, "1.0 kB"},
		{1_500_000, size.SI, "1.5 MB"},
		{-1024, size.IEC, "-1.0 KiB"},
	}
	for _, tc := range cases {
		got := size.Format(tc.bytes, tc.unit)
		if got != tc.want {
			t.Errorf("Format(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestTotalsExcludeActiveContainerReclaim(t *testing.T) {
	for _, state := range []string{"running", "paused", "restarting", "removing", "unknown", ""} {
		t.Run(state, func(t *testing.T) {
			s := &scan.Snapshot{Containers: []docker.Container{{ID: "c", State: state, SizeRw: 100}}, LogSizes: map[string]int64{"c": 20}}
			if got := size.ComputeTotals(s).Reclaimable; got != 0 {
				t.Fatalf("active container counted reclaimable: %d", got)
			}
		})
	}
}

func TestImageTotalIncludesSharedLayersOnce(t *testing.T) {
	s := &scan.Snapshot{LayersSize: 150, LayersSizeKnown: true, Images: []docker.Image{
		{ID: "a", Size: 100, SharedSize: 50}, {ID: "b", Size: 100, SharedSize: 50},
	}}
	if got := size.ComputeTotals(s).Images; got != 150 {
		t.Fatalf("image total = %d, want unique daemon layers 150", got)
	}
}

func TestSharedBuildCacheIsNotReclaimable(t *testing.T) {
	s := &scan.Snapshot{BuildCache: []docker.BuildCacheEntry{{ID: "shared", Size: 10, Shared: true}, {ID: "exclusive", Size: 45}, {ID: "busy", Size: 20, InUse: true}}}
	got := size.ComputeTotals(s)
	if got.BuildCache != 75 || got.Reclaimable != 45 {
		t.Fatalf("build cache accounting: %+v", got)
	}
}
