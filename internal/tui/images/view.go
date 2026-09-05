package images

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	backendimages "github.com/petar030/ssh-native-docker-tui/internal/backend/images"
	"github.com/petar030/ssh-native-docker-tui/internal/tui/ui"
)

func (model Model) View() string {
	if !model.hasData {
		message := "Loading images…"
		if model.err != nil {
			message = "Images unavailable\n\n" + ui.ErrorNotice(model.err.Error(), max(model.width-6, 1)) + "\n\nPress r to retry."
		}
		return fit(lipgloss.NewStyle().Padding(2, 3).Render(message), model.width, model.height)
	}
	var content string
	if model.width >= 110 {
		left := model.listView(max(model.width*3/5, 64), model.height)
		right := model.detailView(max(model.width-lipgloss.Width(left)-1, 38))
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else {
		h := max(model.height/2, 8)
		content = lipgloss.JoinVertical(lipgloss.Left, fit(model.listView(model.width, h), model.width, h), model.detailView(model.width))
	}
	if model.overlay != noOverlay {
		content = ui.OverlayCentered(fit(content, model.width, model.height), model.overlayView(), model.width, model.height)
	}
	return fit(content, model.width, model.height)
}

func (model Model) listView(width, height int) string {
	values := model.visible()
	inner := max(width-4, 1)
	rows := []string{"Filter: " + cell(model.filter, max(inner-28, 8)) + "  Kind: " + model.danglingName() + "  Sort: " + model.sortName(), "", lipgloss.NewStyle().Foreground(ui.Primary).Render("  " + cell("REPOSITORY:TAG", max(inner*38/100, 16)) + " " + cell("IMAGE ID", 12) + " " + cell("SIZE", 10) + " " + cell("CREATED", 16) + " USED")}
	capacity := max(height-6, 1)
	start := max(0, min(model.listStart, max(len(values)-capacity, 0)))
	end := min(start+capacity, len(values))
	for _, value := range values[start:end] {
		marker := " "
		if value.ID == model.selected {
			marker = ">"
		}
		line := marker + " " + cell(imageName(value), max(inner*38/100, 16)) + " " + cell(shortID(value.ID), 12) + " " + cell(ui.FormatBytes(value.Size), 10) + " " + cell(timeText(value.Created), 16) + fmt.Sprintf(" %d", value.Containers)
		if value.ID == model.selected {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAF6FF")).Background(lipgloss.Color("#10365C")).Render(line)
		}
		rows = append(rows, line)
	}
	if len(values) == 0 {
		rows = append(rows, "", "No images match the current filter.")
	}
	count := fmt.Sprintf("%d–%d / %d", zeroStart(start, len(values)), end, len(values))
	return panel("▧  IMAGES"+strings.Repeat(" ", max(inner-8-len(count), 1))+count, strings.Join(rows, "\n"), width)
}

func (model Model) detailView(width int) string {
	if model.panel == historyPanel {
		return model.historyView(width)
	}
	if model.selected == "" {
		return panel("IMAGE DETAILS", "No image selected.", width)
	}
	if model.detailsState.loading && model.detailsID != model.selected {
		return panel("IMAGE DETAILS", "Loading details…", width)
	}
	if model.detailsState.err != nil && model.detailsID != model.selected {
		return panel("IMAGE DETAILS", ui.ErrorNotice(model.detailsState.err.Error(), width-4), width)
	}
	if model.detailsID != model.selected {
		return panel("IMAGE DETAILS", "Waiting for selected image details…", width)
	}
	d := model.details
	lines := []string{"ID:           " + shortID(d.ID), "Tags:         " + dash(strings.Join(d.RepoTags, ", ")), "Created:      " + timeText(d.Created), "Size:         " + ui.FormatBytes(d.Size), "Platform:     " + dash(d.OS+"/"+d.Architecture), "Author:       " + dash(d.Author), "User:         " + dash(d.User), "Working dir:  " + dash(d.WorkingDir), "Ports:        " + dash(strings.Join(d.ExposedPorts, ", ")), "Driver:       " + dash(d.GraphDriver)}
	if len(d.Environment) > 0 {
		lines = append(lines, "", "Environment (values masked):")
		for _, entry := range d.Environment {
			lines = append(lines, "  "+maskEnvironment(entry))
		}
	}
	if len(d.Labels) > 0 {
		lines = append(lines, "", "Labels:")
		for _, pair := range sortedMap(d.Labels) {
			lines = append(lines, "  "+pair)
		}
	}
	return panel("ⓘ  IMAGE DETAILS", strings.Join(lines, "\n"), width)
}
func (model Model) historyView(width int) string {
	if model.historyState.loading && model.history.ImageID != model.selected {
		return panel("HISTORY", "Loading history…", width)
	}
	if model.historyState.err != nil {
		return panel("HISTORY", ui.ErrorNotice(model.historyState.err.Error(), width-4), width)
	}
	if model.history.ImageID != model.selected {
		return panel("HISTORY", "No history loaded.", width)
	}
	rows := []string{"SIZE        CREATED           INSTRUCTION"}
	for _, entry := range model.history.Entries {
		rows = append(rows, cell(ui.FormatBytes(entry.Size), 11)+cell(timeText(entry.Created), 18)+safe(entry.CreatedBy))
	}
	if len(model.history.Entries) == 0 {
		rows = append(rows, "No history entries.")
	}
	return panel("▤  HISTORY", strings.Join(rows, "\n"), width)
}

func (model Model) overlayView() string {
	width := max(min(model.width-12, 76), 44)
	switch model.overlay {
	case filterOverlay:
		return panel("FILTER IMAGES", "> "+safe(model.filterEdit)+"_\n\nenter apply   esc cancel", width)
	case tagOverlay:
		return panel("TAG IMAGE", "Reference (repository:tag):\n> "+safe(model.editPrimary)+"_\n\nenter submit   esc cancel", width)
	case removeOverlay:
		return panel("REMOVE IMAGE", "Remove "+safe(imageName(model.selectedSummary()))+"?\n\n[f] force: "+fmt.Sprint(model.removeForce)+"\n[c] prune children: "+fmt.Sprint(model.removeParents)+"\n\ny/enter confirm   n/esc cancel", width)
	case pruneOverlay:
		return panel("PRUNE IMAGES", "At least one scope is required. dangling=false is never submitted.\n\n[d] dangling=true: "+fmt.Sprint(model.pruneDangling)+"\n"+field("Until", model.editPrimary, model.field == 0)+"\n"+field("Label key", model.editLabelKey, model.field == 1)+"\n"+field("Label value", model.editLabelVal, model.field == 2)+"\n\ntab field   enter submit   esc cancel\n"+notice(model.notice, width), width)
	case pullOverlay:
		return panel("PULL IMAGE", field("Reference", model.editPrimary, model.field == 0)+"\n"+field("Platform os/arch[/variant]", model.editSecondary, model.field == 1)+"\n\ntab field   enter start job   esc cancel\n"+notice(model.notice, width), width)
	}
	return ""
}

func (model Model) selectedSummary() backendimages.Summary {
	for _, value := range model.images {
		if value.ID == model.selected {
			return value
		}
	}
	return backendimages.Summary{}
}
func maskEnvironment(value string) string {
	key, _, ok := strings.Cut(value, "=")
	if !ok {
		return safe(value)
	}
	return safe(key) + "=••••"
}
func sortedMap(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, safe(key)+"="+safe(values[key]))
	}
	return result
}
func field(label, value string, active bool) string {
	marker := " "
	if active {
		marker = ">"
	}
	return marker + " " + label + ": " + safe(value) + "_"
}
func notice(value string, width int) string {
	if value == "" {
		return ""
	}
	return ui.ErrorNotice(value, width-4)
}
func panel(title, body string, width int) string {
	inner := max(width-4, 1)
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = ui.Truncate(lines[i], inner)
	}
	return lipgloss.NewStyle().Width(max(width, 1)).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).Render(lipgloss.NewStyle().Bold(true).Foreground(ui.Primary).Render(ui.Truncate(title, inner)) + "\n" + strings.Join(lines, "\n"))
}
func fit(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ui.Truncate(lines[i], max(width, 1))
	}
	return strings.Join(lines, "\n")
}
func cell(value string, width int) string {
	value = ui.Truncate(safe(value), max(width, 1))
	return value + strings.Repeat(" ", max(width-lipgloss.Width(value), 0))
}
func dash(value string) string {
	value = strings.TrimSpace(safe(value))
	if value == "" || value == "/" {
		return "—"
	}
	return value
}
func safe(value string) string { return ui.SanitizeLine(value) }
func timeText(value interface{ Format(string) string }) string {
	return value.Format("2006-01-02 15:04")
}
func zeroStart(start, length int) int {
	if length == 0 {
		return 0
	}
	return start + 1
}
func (model Model) danglingName() string {
	if model.dangling == 1 {
		return "dangling"
	}
	if model.dangling == 0 {
		return "tagged"
	}
	return "all"
}
func (model Model) sortName() string { return [...]string{"reference", "size", "newest"}[model.sort] }
