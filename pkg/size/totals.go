package size

import (
	"github.com/tyutyutyu/dodu/pkg/docker"
	"github.com/tyutyutyu/dodu/pkg/scan"
)

// Totals aggregates disk usage across all object kinds.
//
// Reclaimable is a conservative estimate: it counts containers in
// stopped states, dangling images (no RepoTags), volumes with RefCount==0,
// and build cache entries that are neither in use nor shared.
type Totals struct {
	Images      int64
	Containers  int64
	Volumes     int64
	BuildCache  int64
	Logs        int64
	Reclaimable int64
}

// ComputeTotals returns Totals for a snapshot.
func ComputeTotals(snap *scan.Snapshot) Totals {
	if snap == nil {
		return Totals{}
	}

	imageSizes := ImageSizes(snap.Images)
	var totalImages, reclaimImages int64
	for _, im := range snap.Images {
		s := imageSizes[im.ID]
		// To avoid double-counting shared bytes across images, count exclusive
		// for images-total. Shared bytes are still attributable but only once
		// at the daemon level (LayersSize).
		totalImages += s.Exclusive
		if isDanglingImage(im) {
			reclaimImages += s.Exclusive
		}
	}

	if snap.LayersSizeKnown && snap.LayersSize >= 0 {
		totalImages = snap.LayersSize
	}

	var totalContainers, totalLogs, reclaimContainers int64
	for _, c := range snap.Containers {
		writable := c.SizeRw
		if writable < 0 {
			writable = 0
		}
		log := snap.LogSize(c.ID)
		totalContainers += writable
		totalLogs += log
		if isStopped(c.State) {
			reclaimContainers += writable + log
		}
	}

	var totalVolumes, reclaimVolumes int64
	for _, v := range snap.Volumes {
		if v.UsageBytes <= 0 {
			continue
		}
		totalVolumes += v.UsageBytes
		if v.RefCount == 0 {
			reclaimVolumes += v.UsageBytes
		}
	}

	var totalBC, reclaimBC int64
	for _, b := range snap.BuildCache {
		totalBC += b.Size
		if !b.InUse && !b.Shared {
			reclaimBC += b.Size
		}
	}

	return Totals{
		Images:      totalImages,
		Containers:  totalContainers,
		Volumes:     totalVolumes,
		BuildCache:  totalBC,
		Logs:        totalLogs,
		Reclaimable: reclaimImages + reclaimContainers + reclaimVolumes + reclaimBC,
	}
}

func isStopped(state string) bool {
	return state == "created" || state == "exited" || state == "dead"
}

func isDanglingImage(im docker.Image) bool {
	return len(im.RepoTags) == 0 || (len(im.RepoTags) == 1 && im.RepoTags[0] == "<none>:<none>")
}
