package docker

import "time"

// DaemonInfo is a minimal projection of `docker info`.
type DaemonInfo struct {
	ID            string
	ServerVersion string
	OS            string
	Arch          string
	Driver        string
}

// Image represents a single image with size accounting fields.
type Image struct {
	ID          string
	RepoTags    []string
	RepoDigests []string
	Created     time.Time
	Size        int64 // total apparent size
	SharedSize  int64 // bytes shared with other images; -1 if unknown
	VirtualSize int64
	Containers  int64 // number of containers using this image (-1 if unknown)
	Labels      map[string]string
	ParentID    string
}

// Container represents a single container with size + log info.
type Container struct {
	ID         string
	Names      []string
	Image      string
	ImageID    string
	Created    time.Time
	State      string // running, exited, ...
	Status     string
	SizeRw     int64 // writable layer size
	SizeRootFs int64 // image + writable layer
	LogPath    string
	Mounts     []ContainerMount
	Labels     map[string]string
}

// ContainerMount describes a single mount on a container.
type ContainerMount struct {
	Type        string // bind, volume, tmpfs
	Name        string // volume name, if Type == "volume"
	Source      string
	Destination string
	RW          bool
}

// Volume represents a Docker volume with optional usage data.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
	Scope      string
	CreatedAt  time.Time
	Labels     map[string]string
	// UsageBytes is the on-disk size; -1 if unknown.
	UsageBytes int64
	// RefCount is the number of containers referencing the volume; -1 if unknown.
	RefCount int64
}

// BuildCacheEntry represents a single build cache record.
type BuildCacheEntry struct {
	ID          string
	Parents     []string
	Type        string
	Description string
	InUse       bool
	Shared      bool
	Size        int64
	CreatedAt   time.Time
	LastUsedAt  time.Time
	UsageCount  int
}

// DiskUsage is the summary returned by `docker system df`.
type DiskUsage struct {
	LayersSize int64
	Images     []Image
	Containers []Container
	Volumes    []Volume
	BuildCache []BuildCacheEntry
}

// Event is a minimal projection of a daemon event used for cache invalidation.
type Event struct {
	Type   string
	Action string
	Actor  string
	Time   time.Time
}

// PruneReport is the result of a prune operation.
type PruneReport struct {
	Deleted        []string
	SpaceReclaimed int64
}

// PruneFilters narrows what can be pruned. Empty means "default".
type PruneFilters struct {
	IDs      []string // build cache IDs only; other prune methods reject this filter
	Labels   map[string]string
	Until    time.Duration // for build cache / images
	Dangling *bool         // for images
}
