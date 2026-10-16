package group_test

import (
	"testing"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/group"
	"github.com/tyutyutyu/dodu/pkg/scan"
)

func sampleSnapshot() *scan.Snapshot {
	return &scan.Snapshot{
		Images: []docker.Image{
			{ID: "img-app", RepoTags: []string{"app:1"}, Size: 1000, SharedSize: 200},
			{ID: "img-db", RepoTags: []string{"db:1"}, Size: 500, SharedSize: 100},
			{ID: "img-orphan", RepoTags: []string{"misc:1"}, Size: 300, SharedSize: 0},
		},
		Containers: []docker.Container{
			{
				ID: "c-app", Names: []string{"/web-app-1"}, ImageID: "img-app", State: "running",
				SizeRw: 50,
				Labels: map[string]string{"com.docker.compose.project": "web", "com.docker.compose.service": "app"},
				Mounts: []docker.ContainerMount{{Type: "volume", Name: "web_data"}},
			},
			{
				ID: "c-db", Names: []string{"/web-db-1"}, ImageID: "img-db", State: "running",
				SizeRw: 30,
				Labels: map[string]string{"com.docker.compose.project": "web", "com.docker.compose.service": "db"},
			},
			{
				ID: "c-loose", Names: []string{"/loose"}, ImageID: "img-orphan", State: "exited",
				SizeRw: 10,
			},
		},
		Volumes: []docker.Volume{
			{Name: "web_data", UsageBytes: 4096, RefCount: 1},
			{Name: "lost", UsageBytes: 2048, RefCount: 0},
		},
		BuildCache: []docker.BuildCacheEntry{
			{ID: "bc1", Size: 100, InUse: false, Description: "RUN apt-get update"},
		},
		LogSizes: map[string]int64{"c-app": 200},
	}
}

func TestByTypeStructure(t *testing.T) {
	root := group.ByType(sampleSnapshot())
	if root.Kind != group.KindRootType {
		t.Fatalf("root kind = %v", root.Kind)
	}
	wantBuckets := map[string]bool{
		"Images": false, "Containers": false, "Volumes": false,
		"Build Cache": false, "Logs": false,
	}
	for _, c := range root.Children {
		wantBuckets[c.Name] = true
	}
	for name, present := range wantBuckets {
		if !present {
			t.Errorf("missing bucket: %s", name)
		}
	}
	// All three containers must show up.
	var containerCount int
	root.Walk(func(n *group.Node) {
		if n.Kind == group.KindContainer {
			containerCount++
		}
	})
	if containerCount != 3 {
		t.Errorf("container count = %d, want 3", containerCount)
	}
}

func TestByTypeImageBacklinks(t *testing.T) {
	root := group.ByType(sampleSnapshot())
	var imgApp *group.Node
	root.Walk(func(n *group.Node) {
		if n.Kind == group.KindImage && n.Refs.ImageID == "img-app" {
			imgApp = n
		}
	})
	if imgApp == nil {
		t.Fatal("img-app node missing")
	}
	if len(imgApp.Refs.ContainerIDs) != 1 || imgApp.Refs.ContainerIDs[0] != "c-app" {
		t.Errorf("img-app refs = %+v", imgApp.Refs)
	}
}

func TestByProjectGrouping(t *testing.T) {
	root := group.ByProject(sampleSnapshot())
	if root.Kind != group.KindRootProject {
		t.Fatalf("root kind = %v", root.Kind)
	}
	projects := map[string]*group.Node{}
	for _, p := range root.Children {
		projects[p.Name] = p
	}
	if _, ok := projects["web"]; !ok {
		t.Fatal("missing project 'web'")
	}
	if _, ok := projects["<orphan>"]; !ok {
		t.Fatal("missing <orphan> bucket")
	}

	// The 'loose' container has no compose label → orphan.
	var seenLoose bool
	projects["<orphan>"].Walk(func(n *group.Node) {
		if n.Name == "loose" {
			seenLoose = true
		}
	})
	if !seenLoose {
		t.Error("loose container should be in <orphan>")
	}

	// 'web' project must contain service nodes for app and db.
	services := map[string]bool{}
	projects["web"].Walk(func(n *group.Node) {
		if n.Kind == group.KindService {
			services[n.Name] = true
		}
	})
	if !services["app"] || !services["db"] {
		t.Errorf("expected app+db services, got %v", services)
	}
}

func TestSortBySize(t *testing.T) {
	root := group.ByType(sampleSnapshot())
	root.Sort(group.SortBySize)
	for _, bucket := range root.Children {
		var prev int64 = 1 << 62
		for _, c := range bucket.Children {
			if c.Size.Exclusive > prev {
				t.Errorf("bucket %q not sorted desc: %d > %d", bucket.Name, c.Size.Exclusive, prev)
			}
			prev = c.Size.Exclusive
		}
	}
}

func TestParentSizeAggregates(t *testing.T) {
	root := group.ByType(sampleSnapshot())
	for _, bucket := range root.Children {
		if bucket.Name != "Images" {
			continue
		}
		var sum int64
		for _, c := range bucket.Children {
			sum += c.Size.Exclusive
		}
		if bucket.Size.Exclusive != sum {
			t.Errorf("Images bucket sum = %d, sum of children = %d", bucket.Size.Exclusive, sum)
		}
	}
}

func TestGroupTotalsCountSharedLayersAndLogsOnce(t *testing.T) {
	snap := &scan.Snapshot{
		LayersSize: 150, LayersSizeKnown: true,
		Images:     []docker.Image{{ID: "a", Size: 100, SharedSize: 50}, {ID: "b", Size: 100, SharedSize: 50}},
		Containers: []docker.Container{{ID: "c", ImageID: "a", SizeRw: 10}},
		LogSizes:   map[string]int64{"c": 20},
	}
	for name, build := range map[string]func(*scan.Snapshot) *group.Node{"type": group.ByType, "project": group.ByProject} {
		t.Run(name, func(t *testing.T) {
			root := build(snap)
			if root.Size.Total != 180 {
				t.Fatalf("total = %d, want 180", root.Size.Total)
			}
			var sum int64
			for _, child := range root.Children {
				sum += child.Size.Total
			}
			if sum != root.Size.Total {
				t.Fatalf("children = %d, root = %d", sum, root.Size.Total)
			}
		})
	}
	root := group.ByType(snap)
	for _, n := range root.Children {
		if n.Name == "Images" && n.Size.Total != 150 {
			t.Errorf("image total = %d", n.Size.Total)
		}
		if n.Name == "Containers" && n.Size.Total != 10 {
			t.Errorf("container total = %d", n.Size.Total)
		}
	}
}

func TestUnknownLayerAggregateUsesExclusiveEstimate(t *testing.T) {
	snap := &scan.Snapshot{Images: []docker.Image{{ID: "a", Size: 100, SharedSize: 50}, {ID: "b", Size: 100, SharedSize: 50}}}
	for _, root := range []*group.Node{group.ByType(snap), group.ByProject(snap)} {
		if root.Size.Total != 100 || !root.Size.Estimated {
			t.Errorf("unknown aggregate = %+v", root.Size)
		}
	}
}

func TestVolumeBacklinksDeduplicateRepeatedMounts(t *testing.T) {
	snap := &scan.Snapshot{
		Containers: []docker.Container{{ID: "c", Mounts: []docker.ContainerMount{{Type: "volume", Name: "v", Destination: "/one"}, {Type: "volume", Name: "v", Destination: "/two"}}}},
		Volumes:    []docker.Volume{{Name: "v", UsageBytes: 10}},
	}
	root := group.ByType(snap)
	root.Walk(func(n *group.Node) {
		if n.Kind == group.KindVolume && len(n.Refs.ContainerIDs) != 1 {
			t.Errorf("volume backlinks = %v", n.Refs.ContainerIDs)
		}
		if n.Kind == group.KindContainer && len(n.Refs.VolumeNames) != 1 {
			t.Errorf("container backlinks = %v", n.Refs.VolumeNames)
		}
	})
}
