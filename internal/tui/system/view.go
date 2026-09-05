package system

import (
	"charm.land/lipgloss/v2"
	"fmt"
	backendsystem "github.com/petar030/ssh-native-docker-tui/internal/backend/system"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
	"strings"
)

func (m Model) View() string {
	if !m.hasInfo && !m.hasDisk {
		v := "Loading system information…"
		if m.err != nil {
			v = "System unavailable\n\n" + ui.ErrorNotice(m.err.Error(), max(m.width-6, 1)) + "\n\nPress r to retry."
		}
		return fit(lipgloss.NewStyle().Padding(2, 3).Render(v), m.width, m.height)
	}
	var out string
	if m.width >= 110 {
		left := m.infoView(max(m.width/2, 55))
		right := m.diskView(max(m.width-lipgloss.Width(left)-1, 54))
		out = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else {
		topHeight := min(6, max(m.height/2, 1))
		out = lipgloss.JoinVertical(lipgloss.Left,
			fit(m.compactInfoView(m.width), m.width, topHeight),
			fit(m.diskView(m.width), m.width, max(m.height-topHeight, 1)),
		)
	}
	if m.reportVisible {
		out = ui.OverlayCentered(fit(out, m.width, m.height), m.reportView(), m.width, m.height)
	}
	if m.overlay != noOverlay {
		out = ui.OverlayCentered(fit(out, m.width, m.height), m.overlayView(), m.width, m.height)
	}
	return fit(out, m.width, m.height)
}

func (m Model) compactInfoView(w int) string {
	if !m.hasInfo {
		return panel("SYSTEM / ENGINE", "Information is loading…", w)
	}
	e, h := m.info.Engine, m.info.Host
	return panel("◆  SYSTEM / ENGINE", strings.Join([]string{
		"Docker " + safe(e.Version) + " · API " + safe(e.APIVersion) + " · " + safe(e.OS) + "/" + safe(e.Architecture),
		"Host " + safe(h.Name) + " · " + safe(h.OperatingSystem) + " · " + safe(h.KernelVersion),
		fmt.Sprintf("%d CPUs · %s memory · %d containers · %d images", h.CPUs, ui.FormatBytes(h.MemoryBytes), h.Containers, h.Images),
	}, "\n"), w)
}
func (m Model) infoView(w int) string {
	if !m.hasInfo {
		return panel("SYSTEM / ENGINE", "Information is loading…", w)
	}
	e, h := m.info.Engine, m.info.Host
	lines := []string{"Docker:       " + safe(e.Version), "API:          " + safe(e.APIVersion) + " (min " + safe(e.MinAPIVersion) + ")", "Platform:     " + safe(e.OS) + "/" + safe(e.Architecture), "Host:         " + safe(h.Name), "OS:           " + safe(h.OperatingSystem) + " " + safe(h.OSVersion), "Kernel:       " + safe(h.KernelVersion), fmt.Sprintf("Resources:    %d CPUs, %s memory", h.CPUs, ui.FormatBytes(h.MemoryBytes)), fmt.Sprintf("Containers:   %d (%d running, %d paused, %d stopped)", h.Containers, h.ContainersRunning, h.ContainersPaused, h.ContainersStopped), fmt.Sprintf("Images:       %d", h.Images), "Storage:      " + unknown(h.StorageDriver), "Logging:      " + unknown(h.LoggingDriver), "Cgroup:       " + unknown(h.CgroupDriver) + " " + unknown(h.CgroupVersion), "Runtime:      " + unknown(h.DefaultRuntime), "Docker root:  " + unknown(h.DockerRootDir), "Plugins:      volume=" + strings.Join(h.Plugins.Volumes, ",") + " network=" + strings.Join(h.Plugins.Networks, ",")}
	for _, warning := range h.Warnings {
		lines = append(lines, ui.ErrorNotice("Docker warning: "+safe(warning), w-4))
	}
	return panel("◆  SYSTEM / ENGINE", strings.Join(lines, "\n"), w)
}
func (m Model) diskView(w int) string {
	if !m.hasDisk {
		return panel("DISK USAGE", "Detailed disk usage is loading…", w)
	}
	if m.diskMode == 0 {
		lines := []string{"RESOURCE      COUNT  ACTIVE  TOTAL        RECLAIMABLE", usage("Containers", m.disk.Containers), usage("Images", m.disk.Images), usage("Volumes", m.disk.Volumes), usage("Build cache", m.disk.BuildCache), "Press i to cycle item details."}
		return panel("▤  DISK USAGE", strings.Join(lines, "\n"), w)
	}
	names := []string{"", "CONTAINERS", "IMAGES", "VOLUMES", "BUILD CACHE"}
	rows := []string{}
	switch m.diskMode {
	case 1:
		for _, v := range m.disk.ContainerItems {
			rows = append(rows, fmt.Sprintf("%-12s %-10s rw=%s root=%s", short(v.ID), safe(v.State), ui.FormatBytes(v.SizeRW), ui.FormatBytes(v.SizeRootFS)))
		}
	case 2:
		for _, v := range m.disk.ImageItems {
			rows = append(rows, fmt.Sprintf("%-18s %s shared=%s", firstTag(v.RepoTags, short(v.ID)), ui.FormatBytes(v.Size), ui.FormatBytes(v.SharedSize)))
		}
	case 3:
		for _, v := range m.disk.VolumeItems {
			size := "unknown or 0 B"
			if v.Size > 0 {
				size = ui.FormatBytes(v.Size)
			}
			rows = append(rows, fmt.Sprintf("%-24s %-10s %s refs=%d", safe(v.Name), safe(v.Driver), size, v.References))
		}
	case 4:
		for _, v := range m.disk.BuildCacheItems {
			rows = append(rows, fmt.Sprintf("%-12s %-10s %s in-use=%t", short(v.ID), safe(v.Type), ui.FormatBytes(v.Size), v.InUse))
		}
	}
	if len(rows) == 0 {
		rows = append(rows, "No items reported.")
	}
	start := min(m.scroll, max(len(rows)-1, 0))
	return panel("▤  "+names[m.diskMode], strings.Join(rows[start:], "\n"), w)
}
func usage(name string, v backendsystem.ResourceDiskUsage) string {
	return fmt.Sprintf("%-13s %5d %7d %12s %12s", name, v.Count, v.Active, ui.FormatBytes(v.TotalBytes), ui.FormatBytes(v.ReclaimableBytes))
}
func (m Model) overlayView() string {
	w := max(min(m.width-12, 86), 52)
	if m.overlay == pruneMenu {
		return panel("CLEAN UP DOCKER", "Choose the resource type first. Each option opens a scoped form; nothing is removed from this menu.\n\n[c] stopped containers\n[i] images\n[v] volumes\n[n] networks\n[s] all unused Docker resources (policy-gated)\n\nesc cancel", w)
	}
	if m.overlay == systemPrune {
		policy := ""
		if m.systemPruneDenied {
			policy = "\n" + ui.ErrorNotice("Unavailable by server policy for this session.", w-4)
		}
		return panel("DANGER: SYSTEM PRUNE", "This can remove all unused containers, networks, images and build cache.\nType exactly:\n"+backendsystem.SystemPruneConfirmation+"\n\n> "+safe(m.fields[0])+"_\n[v] include volumes: "+fmt.Sprint(m.includeVolumes)+policy+"\n\nenter submit   esc cancel\n"+notice(m.notice, w), w)
	}
	title := "PRUNE"
	copy := "At least one filter is required."
	fieldNames := []string{"Older than", "Label key", "Label value"}
	if m.overlay == containerPrune {
		title = "PRUNE STOPPED CONTAINERS"
	}
	if m.overlay == networkPrune {
		title = "PRUNE NETWORKS"
	}
	if m.overlay == imagePrune {
		title = "PRUNE IMAGES"
		copy = "Only dangling images are eligible when enabled. Filters are optional."
	}
	if m.overlay == volumePrune {
		title = "PRUNE VOLUMES"
		copy = "A label is always required. All only broadens matching named volumes."
		fieldNames = []string{"Label key", "Label value"}
	}
	if m.overlay == containerPrune || m.overlay == networkPrune {
		copy = "Older than and/or a label is required."
	}
	fieldValues := m.fields[:]
	if m.overlay == volumePrune {
		fieldValues = m.fields[:2]
	}
	lines := fields(fieldNames, fieldValues, m.field)
	if m.overlay == imagePrune {
		lines = "[d] dangling images only: " + fmt.Sprint(m.dangling) + "\n" + lines
	}
	if m.overlay == volumePrune {
		lines += "\n[a] all named: " + fmt.Sprint(m.allVolumes)
	}
	return panel(title, copy+"\n\n"+lines+"\n\ntab field   enter submit   esc cancel\n"+notice(m.notice, w), w)
}
func (m Model) reportView() string {
	w := max(min(m.width-12, 88), 52)
	r := m.report
	lines := []string{fmt.Sprintf("Containers deleted: %d", len(r.ContainersDeleted)), fmt.Sprintf("Images deleted/untagged: %d", len(r.ImagesDeleted)), fmt.Sprintf("Volumes deleted: %d", len(r.VolumesDeleted)), fmt.Sprintf("Networks deleted: %d", len(r.NetworksDeleted)), fmt.Sprintf("Build cache deleted: %d", len(r.BuildCacheDeleted)), "Space reclaimed: " + ui.FormatBytes(int64(r.SpaceReclaimed)), "", "esc close report"}
	for _, v := range r.ContainersDeleted {
		lines = append(lines, "container: "+safe(v))
	}
	for _, v := range r.ImagesDeleted {
		lines = append(lines, "image: "+safe(v.Deleted+v.Untagged))
	}
	for _, v := range r.VolumesDeleted {
		lines = append(lines, "volume: "+safe(v))
	}
	for _, v := range r.NetworksDeleted {
		lines = append(lines, "network: "+safe(v))
	}
	return panel("PRUNE REPORT", strings.Join(lines, "\n"), w)
}
func fields(n, v []string, a int) string {
	o := make([]string, len(n))
	for i := range n {
		mark := " "
		if i == a {
			mark = ">"
		}
		o[i] = mark + " " + n[i] + ": " + safe(v[i]) + "_"
	}
	return strings.Join(o, "\n")
}
func firstTag(v []string, f string) string {
	if len(v) > 0 {
		return safe(v[0])
	}
	return f
}
func short(v string) string {
	v = strings.TrimPrefix(v, "sha256:")
	if len(v) > 12 {
		return v[:12]
	}
	return v
}
func unknown(v string) string {
	v = strings.TrimSpace(safe(v))
	if v == "" {
		return "—"
	}
	return v
}
func safe(v string) string { return ui.SanitizeLine(v) }
func notice(v string, w int) string {
	if v == "" {
		return ""
	}
	return ui.ErrorNotice(v, w-4)
}
func panel(t, b string, w int) string {
	i := max(w-4, 1)
	l := strings.Split(b, "\n")
	for n := range l {
		l[n] = ui.Truncate(l[n], i)
	}
	return lipgloss.NewStyle().Width(max(w, 1)).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render(ui.Truncate(t, i)) + "\n" + strings.Join(l, "\n"))
}
func fit(v string, w, h int) string {
	l := strings.Split(v, "\n")
	if h > 0 && len(l) > h {
		l = l[:h]
	}
	for i := range l {
		l[i] = ui.Truncate(l[i], max(w, 1))
	}
	return strings.Join(l, "\n")
}
