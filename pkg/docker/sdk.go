package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
)

// New constructs a Client backed by the official Docker Engine SDK.
func New(opts ...Option) (Client, error) {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}

	clientOpts := []dockerclient.Opt{
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	}
	if cfg.host != "" {
		clientOpts = append(clientOpts, dockerclient.WithHost(cfg.host))
	}
	if cfg.timeout > 0 {
		clientOpts = append(clientOpts, dockerclient.WithTimeout(cfg.timeout))
	}

	cli, err := dockerclient.NewClientWithOpts(clientOpts...)
	if err != nil {
		return nil, mapError(err)
	}
	return &sdkClient{cli: cli, opts: cfg}, nil
}

type sdkClient struct {
	cli  *dockerclient.Client
	opts options
}

func (c *sdkClient) Close() error {
	if c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

func (c *sdkClient) Ping(ctx context.Context) (DaemonInfo, error) {
	info, err := c.cli.Info(ctx)
	if err != nil {
		return DaemonInfo{}, mapError(err)
	}
	return DaemonInfo{
		ID:            info.ID,
		ServerVersion: info.ServerVersion,
		OS:            info.OperatingSystem,
		Arch:          info.Architecture,
		Driver:        info.Driver,
	}, nil
}

func (c *sdkClient) ListImages(ctx context.Context) ([]Image, error) {
	raw, err := c.cli.ImageList(ctx, image.ListOptions{
		All:        true,
		SharedSize: true,
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]Image, 0, len(raw))
	for _, im := range raw {
		out = append(out, Image{
			ID:          im.ID,
			RepoTags:    im.RepoTags,
			RepoDigests: im.RepoDigests,
			Created:     time.Unix(im.Created, 0),
			Size:        im.Size,
			SharedSize:  im.SharedSize,
			Containers:  im.Containers,
			Labels:      im.Labels,
			ParentID:    im.ParentID,
		})
	}
	return out, nil
}

func (c *sdkClient) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	raw, err := c.cli.ContainerList(ctx, container.ListOptions{
		All:  all,
		Size: true,
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]Container, 0, len(raw))
	for _, ct := range raw {
		mounts := make([]ContainerMount, 0, len(ct.Mounts))
		for _, m := range ct.Mounts {
			mounts = append(mounts, ContainerMount{
				Type:        string(m.Type),
				Name:        m.Name,
				Source:      m.Source,
				Destination: m.Destination,
				RW:          m.RW,
			})
		}
		out = append(out, Container{
			ID:         ct.ID,
			Names:      ct.Names,
			Image:      ct.Image,
			ImageID:    ct.ImageID,
			Created:    time.Unix(ct.Created, 0),
			State:      ct.State,
			Status:     ct.Status,
			SizeRw:     ct.SizeRw,
			SizeRootFs: ct.SizeRootFs,
			Mounts:     mounts,
			Labels:     ct.Labels,
		})
	}
	return out, nil
}

func (c *sdkClient) ListVolumes(ctx context.Context) ([]Volume, error) {
	resp, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{
		Types: []types.DiskUsageObject{types.VolumeObject},
	})
	if err != nil {
		return nil, mapError(err)
	}
	return c.volumesWithUsage(ctx, resp.Volumes)
}

// Disk usage omits plugin volumes and local volumes with mount options. Keep
// the full inventory and enrich only entries with measured usage.
func (c *sdkClient) volumesWithUsage(ctx context.Context, measured []*volume.Volume) ([]Volume, error) {
	resp, err := c.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, mapError(err)
	}
	usageByName := make(map[string]*volume.UsageData, len(measured))
	for _, v := range measured {
		if v != nil {
			usageByName[v.Name] = v.UsageData
		}
	}
	out := make([]Volume, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		if v == nil {
			continue
		}
		var (
			usage    int64 = -1
			refCount int64 = -1
		)
		if data := usageByName[v.Name]; data != nil {
			usage = data.Size
			refCount = data.RefCount
		}
		created, _ := time.Parse(time.RFC3339, v.CreatedAt)
		out = append(out, Volume{
			Name:       v.Name,
			Driver:     v.Driver,
			Mountpoint: v.Mountpoint,
			Scope:      v.Scope,
			CreatedAt:  created,
			Labels:     v.Labels,
			UsageBytes: usage,
			RefCount:   refCount,
		})
	}
	return out, nil
}

func (c *sdkClient) BuildCacheUsage(ctx context.Context) ([]BuildCacheEntry, error) {
	du, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{
		Types: []types.DiskUsageObject{types.BuildCacheObject},
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]BuildCacheEntry, 0, len(du.BuildCache))
	for _, b := range du.BuildCache {
		if b == nil {
			continue
		}
		out = append(out, BuildCacheEntry{
			ID:          b.ID,
			Parents:     b.Parents,
			Type:        b.Type,
			Description: b.Description,
			InUse:       b.InUse,
			Shared:      b.Shared,
			Size:        b.Size,
			CreatedAt:   b.CreatedAt,
			LastUsedAt:  derefTime(b.LastUsedAt),
			UsageCount:  b.UsageCount,
		})
	}
	return out, nil
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (c *sdkClient) DiskUsage(ctx context.Context) (DiskUsage, error) {
	du, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return DiskUsage{}, mapError(err)
	}
	out := DiskUsage{LayersSize: du.LayersSize}
	out.Volumes, err = c.volumesWithUsage(ctx, du.Volumes)
	if err != nil {
		return DiskUsage{}, err
	}
	for _, im := range du.Images {
		if im == nil {
			continue
		}
		out.Images = append(out.Images, Image{
			RepoDigests: im.RepoDigests,
			ID:          im.ID,
			RepoTags:    im.RepoTags,
			Created:     time.Unix(im.Created, 0),
			Size:        im.Size,
			SharedSize:  im.SharedSize,
			Containers:  im.Containers,
			Labels:      im.Labels,
			ParentID:    im.ParentID,
		})
	}
	for _, ct := range du.Containers {
		if ct == nil {
			continue
		}
		mounts := make([]ContainerMount, 0, len(ct.Mounts))
		for _, m := range ct.Mounts {
			mounts = append(mounts, ContainerMount{Type: string(m.Type), Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW})
		}
		out.Containers = append(out.Containers, Container{
			Mounts:     mounts,
			ID:         ct.ID,
			Names:      ct.Names,
			Image:      ct.Image,
			ImageID:    ct.ImageID,
			Created:    time.Unix(ct.Created, 0),
			State:      ct.State,
			Status:     ct.Status,
			SizeRw:     ct.SizeRw,
			SizeRootFs: ct.SizeRootFs,
			Labels:     ct.Labels,
		})
	}
	for _, b := range du.BuildCache {
		if b == nil {
			continue
		}
		out.BuildCache = append(out.BuildCache, BuildCacheEntry{
			ID:          b.ID,
			Parents:     b.Parents,
			Type:        b.Type,
			Description: b.Description,
			InUse:       b.InUse,
			Shared:      b.Shared,
			Size:        b.Size,
			CreatedAt:   b.CreatedAt,
			LastUsedAt:  derefTime(b.LastUsedAt),
			UsageCount:  b.UsageCount,
		})
	}
	return out, nil
}

func (c *sdkClient) LogFileSize(ctx context.Context, containerID string) (int64, error) {
	if containerID == "" {
		return 0, errors.New("empty container id")
	}
	insp, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return 0, mapError(err)
	}
	// Paths returned by a remote daemon belong to that host, not this client.
	if !strings.HasPrefix(c.cli.DaemonHost(), "unix://") || insp.ContainerJSONBase == nil || insp.LogPath == "" {
		return 0, nil
	}
	st, err := os.Stat(insp.LogPath)
	if err != nil {
		// Common: docker rootless or Docker Desktop hides the path; treat as 0.
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat log file: %w", err)
	}
	return st.Size(), nil
}

func (c *sdkClient) PruneImages(ctx context.Context, f PruneFilters) (PruneReport, error) {
	if len(f.IDs) > 0 {
		return PruneReport{}, errors.New("ID filters are supported only for build cache; use individual remove operations")
	}
	args := pruneArgs(f, withDangling)
	rep, err := c.cli.ImagesPrune(ctx, args)
	if err != nil {
		return PruneReport{}, mapError(err)
	}
	deleted := make([]string, 0, len(rep.ImagesDeleted))
	for _, d := range rep.ImagesDeleted {
		switch {
		case d.Deleted != "":
			deleted = append(deleted, d.Deleted)
		case d.Untagged != "":
			deleted = append(deleted, d.Untagged)
		}
	}
	return PruneReport{
		Deleted:        deleted,
		SpaceReclaimed: int64(rep.SpaceReclaimed), //nolint:gosec // Docker reports unsigned bytes.
	}, nil
}

func (c *sdkClient) PruneContainers(ctx context.Context, f PruneFilters) (PruneReport, error) {
	if len(f.IDs) > 0 {
		return PruneReport{}, errors.New("ID filters are supported only for build cache; use individual remove operations")
	}
	rep, err := c.cli.ContainersPrune(ctx, pruneArgs(f))
	if err != nil {
		return PruneReport{}, mapError(err)
	}
	return PruneReport{
		Deleted:        rep.ContainersDeleted,
		SpaceReclaimed: int64(rep.SpaceReclaimed), //nolint:gosec
	}, nil
}

func (c *sdkClient) PruneVolumes(ctx context.Context, f PruneFilters) (PruneReport, error) {
	if len(f.IDs) > 0 {
		return PruneReport{}, errors.New("ID filters are supported only for build cache; use individual remove operations")
	}
	rep, err := c.cli.VolumesPrune(ctx, pruneArgs(f))
	if err != nil {
		return PruneReport{}, mapError(err)
	}
	return PruneReport{
		Deleted:        rep.VolumesDeleted,
		SpaceReclaimed: int64(rep.SpaceReclaimed), //nolint:gosec
	}, nil
}

func (c *sdkClient) PruneBuildCache(ctx context.Context, f PruneFilters) (PruneReport, error) {
	args := pruneArgs(f)
	if len(f.IDs) > 0 {
		ids := make([]string, 0, len(f.IDs))
		for _, id := range f.IDs {
			if id == "" {
				return PruneReport{}, errors.New("empty build cache id")
			}
			ids = append(ids, regexp.QuoteMeta(id))
		}
		// Engine translates its single id filter into a BuildKit regular expression.
		// Anchor and escape it so selection never widens to substring/regex matches.
		args.Add("id", "^("+strings.Join(ids, "|")+")$")
	}
	rep, err := c.cli.BuildCachePrune(ctx, types.BuildCachePruneOptions{
		Filters: args,
		// Explicit selection includes internal/frontend/shared records; the exact
		// ID filter still bounds deletion. Preserve default retention without IDs.
		All: len(f.IDs) > 0,
	})
	if err != nil {
		return PruneReport{}, mapError(err)
	}
	return PruneReport{
		Deleted:        rep.CachesDeleted,
		SpaceReclaimed: int64(rep.SpaceReclaimed), //nolint:gosec
	}, nil
}

type pruneOpt int

const withDangling pruneOpt = 1

// RemoveImage deletes an image by ID and returns reclaimed bytes.
func (c *sdkClient) RemoveImage(ctx context.Context, id string, force, pruneChildren bool) (int64, error) {
	resp, err := c.cli.ImageRemove(ctx, id, image.RemoveOptions{
		Force:         force,
		PruneChildren: pruneChildren,
	})
	if err != nil {
		return 0, mapError(err)
	}
	// Docker doesn't return reclaimed bytes per-image; return 0 (caller uses estimate).
	_ = resp
	return 0, nil
}

// RemoveContainer deletes a container by ID.
func (c *sdkClient) RemoveContainer(ctx context.Context, id string, force, removeVolumes bool) error {
	err := c.cli.ContainerRemove(ctx, id, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: removeVolumes,
	})
	if err != nil {
		return mapError(err)
	}
	return nil
}

// RemoveVolume deletes a volume by name.
func (c *sdkClient) RemoveVolume(ctx context.Context, name string, force bool) error {
	if err := c.cli.VolumeRemove(ctx, name, force); err != nil {
		return mapError(err)
	}
	return nil
}

func pruneArgs(f PruneFilters, opts ...pruneOpt) filters.Args {
	args := filters.NewArgs()
	for k, v := range f.Labels {
		args.Add("label", fmt.Sprintf("%s=%s", k, v))
	}
	if f.Until > 0 {
		args.Add("until", f.Until.String())
	}
	for _, o := range opts {
		if o == withDangling && f.Dangling != nil {
			if *f.Dangling {
				args.Add("dangling", "true")
			} else {
				args.Add("dangling", "false")
			}
		}
	}
	return args
}
