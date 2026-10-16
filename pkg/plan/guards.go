package plan

import (
	"fmt"
	"sort"

	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/group"
	"github.com/tyutyutyu/dodu/pkg/scan"
	"github.com/tyutyutyu/dodu/pkg/size"
)

// Build evaluates marks against the snapshot and returns a safe Plan:
// active containers and the images/volumes they depend on are blocked;
// in-use volumes are blocked; tagged image relations are warned.
func Build(snap *scan.Snapshot, marks []Mark) *Plan {
	p := &Plan{}
	if snap == nil {
		return p
	}

	imgByID := map[string]docker.Image{}
	for _, im := range snap.Images {
		imgByID[im.ID] = im
	}
	cByID := map[string]docker.Container{}
	for _, c := range snap.Containers {
		cByID[c.ID] = c
	}
	volByName := map[string]docker.Volume{}
	for _, v := range snap.Volumes {
		volByName[v.Name] = v
	}

	// Image -> active or unknown-state containers using it
	runningByImage := map[string][]string{}
	// Volume -> running containers using it
	runningByVolume := map[string][]string{}
	// Volume -> any containers using it
	usedVolumes := map[string]bool{}
	for _, c := range snap.Containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				usedVolumes[m.Name] = true
				if !stopped(c.State) {
					runningByVolume[m.Name] = append(runningByVolume[m.Name], c.ID)
				}
			}
		}
		if !stopped(c.State) && c.ImageID != "" {
			runningByImage[c.ImageID] = append(runningByImage[c.ImageID], c.ID)
		}
	}

	imgSizes := size.ImageSizes(snap.Images)

	seen := make(map[Mark]bool)
	for _, mk := range marks {
		if seen[mk] {
			continue
		}
		seen[mk] = true
		switch mk.Kind {
		case group.KindImage:
			im, ok := imgByID[mk.ID]
			if !ok {
				p.Warnings = append(p.Warnings, fmt.Sprintf("image %s not found in snapshot", short(mk.ID)))
				continue
			}
			est := imgSizes[mk.ID].Exclusive
			if running := runningByImage[mk.ID]; len(running) > 0 {
				p.Blocked = append(p.Blocked, Item{
					Kind: mk.Kind, ID: mk.ID,
					Name:       displayImage(im),
					EstReclaim: est,
					Reason:     fmt.Sprintf("image is used by active or unknown-state container(s): %v", short(running...)),
				})
				continue
			}
			if len(im.RepoTags) > 0 && im.Containers > 0 {
				p.Warnings = append(p.Warnings, fmt.Sprintf("tagged image %s referenced by %d container(s)", displayImage(im), im.Containers))
			}
			p.Items = append(p.Items, Item{
				Kind: mk.Kind, ID: mk.ID,
				Name:       displayImage(im),
				EstReclaim: est,
				Reason:     "marked for deletion",
			})

		case group.KindContainer:
			c, ok := cByID[mk.ID]
			if !ok {
				p.Warnings = append(p.Warnings, fmt.Sprintf("container %s not found", short(mk.ID)))
				continue
			}
			writable := c.SizeRw
			if writable < 0 {
				writable = 0
			}
			est := writable + snap.LogSize(c.ID)
			if !stopped(c.State) {
				p.Blocked = append(p.Blocked, Item{
					Kind: mk.Kind, ID: mk.ID,
					Name:       displayContainer(c),
					EstReclaim: est,
					Reason:     "container is active or its state is unknown; stop it first",
				})
				continue
			}
			p.Items = append(p.Items, Item{
				Kind: mk.Kind, ID: mk.ID,
				Name:       displayContainer(c),
				EstReclaim: est,
				Reason:     "stopped container",
			})

		case group.KindVolume:
			v, ok := volByName[mk.ID]
			if !ok {
				p.Warnings = append(p.Warnings, fmt.Sprintf("volume %s not found", mk.ID))
				continue
			}
			est := v.UsageBytes
			if est < 0 {
				est = 0
			}
			if usedVolumes[mk.ID] || v.RefCount > 0 {
				p.Blocked = append(p.Blocked, Item{
					Kind: mk.Kind, ID: mk.ID,
					Name:       v.Name,
					EstReclaim: est,
					Reason:     "volume is in use",
				})
				continue
			}
			p.Items = append(p.Items, Item{
				Kind: mk.Kind, ID: mk.ID,
				Name:       v.Name,
				EstReclaim: est,
				Reason:     "unused volume",
			})

		case group.KindBuildCache:
			var entry *docker.BuildCacheEntry
			for i := range snap.BuildCache {
				if snap.BuildCache[i].ID == mk.ID {
					entry = &snap.BuildCache[i]
					break
				}
			}
			if entry == nil {
				p.Warnings = append(p.Warnings, fmt.Sprintf("build cache %s not found", short(mk.ID)))
				continue
			}
			est := entry.Size
			if entry.Shared {
				est = 0
			}
			if entry.InUse {
				p.Blocked = append(p.Blocked, Item{
					Kind: mk.Kind, ID: mk.ID,
					Name:       displayBC(*entry),
					EstReclaim: est,
					Reason:     "build cache entry is in use",
				})
				continue
			}
			p.Items = append(p.Items, Item{
				Kind: mk.Kind, ID: mk.ID,
				Name:       displayBC(*entry),
				EstReclaim: est,
				Reason:     "unused build cache",
			})

		default:
			p.Warnings = append(p.Warnings, fmt.Sprintf("unsupported kind %s for id %s", mk.Kind, mk.ID))
		}
	}

	// Remove selected stopped containers before their images.
	sort.SliceStable(p.Items, func(i, j int) bool {
		return p.Items[i].Kind == group.KindContainer && p.Items[j].Kind != group.KindContainer
	})
	for _, it := range p.Items {
		p.EstReclaim += it.EstReclaim
	}
	return p
}

func short(ids ...string) string {
	if len(ids) == 0 {
		return ""
	}
	if len(ids) == 1 {
		id := ids[0]
		if len(id) > 12 {
			return id[:12]
		}
		return id
	}
	out := "["
	for i, id := range ids {
		if i > 0 {
			out += ", "
		}
		if len(id) > 12 {
			id = id[:12]
		}
		out += id
	}
	return out + "]"
}

func displayImage(im docker.Image) string {
	for _, t := range im.RepoTags {
		if t != "" && t != "<none>:<none>" {
			return t
		}
	}
	return short(im.ID)
}

func displayContainer(c docker.Container) string {
	for _, n := range c.Names {
		return n
	}
	return short(c.ID)
}

func displayBC(b docker.BuildCacheEntry) string {
	if b.Description != "" {
		return b.Description
	}
	return short(b.ID)
}

// Only terminal or never-started containers are safe deletion candidates.
func stopped(state string) bool {
	return state == "exited" || state == "created" || state == "dead"
}
