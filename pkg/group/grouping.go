package group

import (
	"slices"
	"strings"

	"github.com/tyutyutyu/dodu/pkg/scan"
	"github.com/tyutyutyu/dodu/pkg/size"
)

const (
	bucketImages     = "Images"
	bucketContainers = "Containers"
	bucketVolumes    = "Volumes"
	bucketBuildCache = "Build Cache"
	bucketLogs       = "Logs"
)

// ByType returns a tree rooted at "by type" with one bucket per object kind.
func ByType(snap *scan.Snapshot) *Node {
	if snap == nil {
		return &Node{Name: "by type", Kind: KindRootType}
	}
	imgSizes := size.ImageSizes(snap.Images)

	root := &Node{Name: "by type", Kind: KindRootType}
	imagesNode := &Node{Name: bucketImages, Kind: KindTypeBucket}
	containersNode := &Node{Name: bucketContainers, Kind: KindTypeBucket}
	volumesNode := &Node{Name: bucketVolumes, Kind: KindTypeBucket}
	buildCacheNode := &Node{Name: bucketBuildCache, Kind: KindTypeBucket}
	logsNode := &Node{Name: bucketLogs, Kind: KindTypeBucket}

	// Build container backlinks before populating image nodes.
	containersByImage := map[string][]string{}
	for _, c := range snap.Containers {
		if c.ImageID != "" {
			containersByImage[c.ImageID] = append(containersByImage[c.ImageID], c.ID)
		}
	}
	containersByVolume := map[string][]string{}
	for _, c := range snap.Containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				if !slices.Contains(containersByVolume[m.Name], c.ID) {
					containersByVolume[m.Name] = append(containersByVolume[m.Name], c.ID)
				}
			}
		}
	}

	for _, im := range snap.Images {
		imagesNode.Children = append(imagesNode.Children, &Node{
			Name: imageDisplayName(im.RepoTags, im.ID),
			Kind: KindImage,
			Size: imgSizes[im.ID],
			Refs: Refs{ImageID: im.ID, ContainerIDs: containersByImage[im.ID]},
			Meta: map[string]string{"id": im.ID},
		})
	}

	for _, c := range snap.Containers {
		writable := c.SizeRw
		if writable < 0 {
			writable = 0
		}
		log := snap.LogSize(c.ID)
		volNames := []string{}
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				if !slices.Contains(volNames, m.Name) {
					volNames = append(volNames, m.Name)
				}
			}
		}
		containersNode.Children = append(containersNode.Children, &Node{
			Name: containerDisplayName(c.Names, c.ID),
			Kind: KindContainer,
			Size: size.ImageSize{Total: writable, Exclusive: writable},
			Refs: Refs{ImageID: c.ImageID, VolumeNames: volNames},
			Meta: map[string]string{
				"id":     c.ID,
				"image":  c.Image,
				"state":  c.State,
				"status": c.Status,
			},
		})

		if log > 0 {
			logsNode.Children = append(logsNode.Children, &Node{
				Name: containerDisplayName(c.Names, c.ID),
				Kind: KindLogFile,
				Size: size.ImageSize{Total: log, Exclusive: log},
				Meta: map[string]string{"container_id": c.ID},
			})
		}
	}

	for _, v := range snap.Volumes {
		usage := v.UsageBytes
		if usage < 0 {
			usage = 0
		}
		volumesNode.Children = append(volumesNode.Children, &Node{
			Name: v.Name,
			Kind: KindVolume,
			Size: size.ImageSize{Total: usage, Exclusive: usage, Estimated: v.UsageBytes < 0},
			Refs: Refs{ContainerIDs: containersByVolume[v.Name]},
			Meta: map[string]string{
				"driver":     v.Driver,
				"mountpoint": v.Mountpoint,
			},
		})
	}

	for _, b := range snap.BuildCache {
		buildCacheNode.Children = append(buildCacheNode.Children, &Node{
			Name: buildCacheDisplayName(b.Description, b.ID),
			Kind: KindBuildCache,
			Size: size.ImageSize{Total: b.Size, Exclusive: b.Size},
			Meta: map[string]string{
				"id":     b.ID,
				"type":   b.Type,
				"in_use": boolStr(b.InUse),
			},
		})
	}

	for _, b := range []*Node{imagesNode, containersNode, volumesNode, buildCacheNode, logsNode} {
		sumChildren(b)
		if b == imagesNode {
			if snap.LayersSizeKnown && snap.LayersSize >= 0 {
				b.Size.Total = snap.LayersSize
				b.Size.Shared = max(int64(0), snap.LayersSize-b.Size.Exclusive)
			} else if len(snap.Images) > 0 {
				b.Size.Estimated = true
			}
		}
		root.Children = append(root.Children, b)
	}
	sumChildren(root)
	if !snap.LayersSizeKnown && len(snap.Images) > 0 {
		root.Size.Estimated = true
	}
	return root
}

// ByProject groups containers by their com.docker.compose.project label, with
// images / volumes attached to the project that owns at least one container
// referencing them. Anything else lands in <orphan>.
func ByProject(snap *scan.Snapshot) *Node {
	if snap == nil {
		return &Node{Name: "by project", Kind: KindRootProject}
	}
	root := &Node{Name: "by project", Kind: KindRootProject}
	projects := map[string]*Node{}
	getProject := func(name string) *Node {
		if name == "" {
			name = "<orphan>"
		}
		if n, ok := projects[name]; ok {
			return n
		}
		n := &Node{Name: name, Kind: KindProject}
		projects[name] = n
		return n
	}

	imgSizes := size.ImageSizes(snap.Images)
	imageByID := map[string]*Node{}
	for _, im := range snap.Images {
		imageByID[im.ID] = &Node{
			Name: imageDisplayName(im.RepoTags, im.ID),
			Kind: KindImage,
			Size: imgSizes[im.ID],
			Refs: Refs{ImageID: im.ID},
			Meta: map[string]string{"id": im.ID},
		}
	}

	imageOwner := map[string]string{}  // image ID → project
	volumeOwner := map[string]string{} // volume name → project
	projectImages := map[string]map[string]bool{}
	projectVolumes := map[string]map[string]bool{}

	for _, c := range snap.Containers {
		project := c.Labels["com.docker.compose.project"]
		service := c.Labels["com.docker.compose.service"]
		pNode := getProject(project)

		writable := c.SizeRw
		if writable < 0 {
			writable = 0
		}
		log := snap.LogSize(c.ID)

		var parent *Node
		if service != "" {
			parent = findOrCreateService(pNode, service)
		} else {
			parent = pNode
		}
		parent.Children = append(parent.Children, &Node{
			Name: containerDisplayName(c.Names, c.ID),
			Kind: KindContainer,
			Size: size.ImageSize{Total: writable + log, Exclusive: writable + log},
			Refs: Refs{ImageID: c.ImageID},
			Meta: map[string]string{
				"id":      c.ID,
				"service": service,
				"state":   c.State,
			},
		})

		if c.ImageID != "" && project != "" {
			imageOwner[c.ImageID] = project
			if projectImages[project] == nil {
				projectImages[project] = map[string]bool{}
			}
			projectImages[project][c.ImageID] = true
		}
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" && project != "" {
				volumeOwner[m.Name] = project
				if projectVolumes[project] == nil {
					projectVolumes[project] = map[string]bool{}
				}
				projectVolumes[project][m.Name] = true
			}
		}
	}

	// Attach images / volumes to their project (or orphan).
	orphan := getProject("")
	for _, im := range snap.Images {
		node, ok := imageByID[im.ID]
		if !ok {
			continue
		}
		owner, hasOwner := imageOwner[im.ID]
		if !hasOwner {
			orphan.Children = append(orphan.Children, node)
			continue
		}
		if projectImages[owner][im.ID] {
			projects[owner].Children = append(projects[owner].Children, node)
		}
	}
	for _, v := range snap.Volumes {
		usage := v.UsageBytes
		if usage < 0 {
			usage = 0
		}
		project := v.Labels["com.docker.compose.project"]
		if project == "" {
			project = volumeOwner[v.Name]
		}
		volNode := &Node{
			Name: v.Name,
			Kind: KindVolume,
			Size: size.ImageSize{Total: usage, Exclusive: usage, Estimated: v.UsageBytes < 0},
		}
		if project == "" {
			orphan.Children = append(orphan.Children, volNode)
		} else {
			getProject(project).Children = append(getProject(project).Children, volNode)
		}
	}
	for _, b := range snap.BuildCache {
		orphan.Children = append(orphan.Children, &Node{
			Name: buildCacheDisplayName(b.Description, b.ID),
			Kind: KindBuildCache,
			Size: size.ImageSize{Total: b.Size, Exclusive: b.Size},
			Meta: map[string]string{"id": b.ID},
		})
	}

	// Shared layers cannot be assigned to a single Compose project. Keep
	// their daemon-wide contribution separate rather than charging every image.
	var exclusiveImages int64
	for _, image := range imgSizes {
		exclusiveImages += image.Exclusive
	}
	if snap.LayersSizeKnown && snap.LayersSize > exclusiveImages {
		root.Children = append(root.Children, &Node{
			Name: "<shared image layers>", Kind: KindProject,
			Size: size.ImageSize{Total: snap.LayersSize - exclusiveImages, Shared: snap.LayersSize - exclusiveImages},
		})
	}
	for _, p := range projects {
		sumChildren(p)
		root.Children = append(root.Children, p)
	}
	sumChildren(root)
	if !snap.LayersSizeKnown && len(snap.Images) > 0 {
		root.Size.Estimated = true
	}
	return root
}

func findOrCreateService(project *Node, service string) *Node {
	for _, c := range project.Children {
		if c.Kind == KindService && c.Name == service {
			return c
		}
	}
	n := &Node{Name: service, Kind: KindService}
	project.Children = append(project.Children, n)
	return n
}

func imageDisplayName(repoTags []string, id string) string {
	for _, t := range repoTags {
		if t != "" && t != "<none>:<none>" {
			return t
		}
	}
	return shortID(id)
}

func containerDisplayName(names []string, id string) string {
	for _, n := range names {
		return strings.TrimPrefix(n, "/")
	}
	return shortID(id)
}

func buildCacheDisplayName(desc, id string) string {
	if desc != "" {
		return desc
	}
	return shortID(id)
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
