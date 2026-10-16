// Package group builds navigable node trees from a scan.Snapshot, in either
// "by type" (Images / Containers / Volumes / BuildCache / Logs) or
// "by compose project" (label com.docker.compose.project) layout.
package group

import (
	"sort"

	"github.com/tyutyutyu/dodu/pkg/size"
)

// Kind classifies a node.
type Kind string

// Node kinds.
const (
	KindRootType    Kind = "root_type"
	KindRootProject Kind = "root_project"
	KindTypeBucket  Kind = "type_bucket"
	KindProject     Kind = "project"
	KindService     Kind = "service"
	KindImage       Kind = "image"
	KindContainer   Kind = "container"
	KindVolume      Kind = "volume"
	KindBuildCache  Kind = "build_cache"
	KindLogFile     Kind = "log_file"
)

// Refs holds backlinks between objects so the TUI can show "used by" lists.
type Refs struct {
	ImageID      string
	ContainerIDs []string
	VolumeNames  []string
}

// Node is a single entry in the tree.
type Node struct {
	Name     string
	Kind     Kind
	Size     size.ImageSize // Total/Shared/Exclusive/Estimated also used for non-images
	Children []*Node
	Refs     Refs
	Meta     map[string]string
}

// SortKey selects how children are ordered.
type SortKey int

// Sort keys.
const (
	SortBySize SortKey = iota
	SortByName
	SortByCount
)

// Sort orders this node's children (recursively) by the given key, descending
// for size/count and ascending for name.
func (n *Node) Sort(by SortKey) {
	if n == nil {
		return
	}
	switch by {
	case SortBySize:
		sort.SliceStable(n.Children, func(i, j int) bool {
			return n.Children[i].Size.Exclusive > n.Children[j].Size.Exclusive
		})
	case SortByCount:
		sort.SliceStable(n.Children, func(i, j int) bool {
			return len(n.Children[i].Children) > len(n.Children[j].Children)
		})
	default:
		sort.SliceStable(n.Children, func(i, j int) bool {
			return n.Children[i].Name < n.Children[j].Name
		})
	}
	for _, c := range n.Children {
		c.Sort(by)
	}
}

// Walk visits every node depth-first.
func (n *Node) Walk(fn func(*Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

func sumChildren(n *Node) {
	if n == nil || len(n.Children) == 0 {
		return
	}
	var total, shared, exclusive int64
	estimated := false
	for _, c := range n.Children {
		sumChildren(c)
		// Image totals include layers shared by other image leaves. Aggregate
		// their exclusive contribution; daemon-wide shared layers are added once.
		if c.Kind == KindImage {
			total += c.Size.Exclusive
		} else {
			total += c.Size.Total
		}
		shared += c.Size.Shared
		exclusive += c.Size.Exclusive
		if c.Size.Estimated {
			estimated = true
		}
	}
	// Only overwrite if the node didn't carry its own size.
	if n.Size.Total == 0 && n.Size.Exclusive == 0 {
		n.Size = size.ImageSize{Total: total, Shared: shared, Exclusive: exclusive, Estimated: estimated}
	}
}
