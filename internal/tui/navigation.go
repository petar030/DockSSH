package tui

import "github.com/petar030/ssh-native-docker-tui/internal/backend"

type tab struct {
	page  backend.Page
	label string
}

var tabs = []tab{
	{page: backend.PageDashboard, label: "Dashboard"},
	{page: backend.PageContainers, label: "Containers"},
	{page: backend.PageCompose, label: "Compose"},
	{page: backend.PageImages, label: "Images"},
	{page: backend.PageVolumes, label: "Volumes"},
	{page: backend.PageNetworks, label: "Networks"},
	{page: backend.PageEvents, label: "Events"},
	{page: backend.PageSystem, label: "System"},
}

func tabFromKey(key string) (int, bool) {
	if len(key) != 1 || key[0] < '1' || key[0] > '8' {
		return 0, false
	}
	return int(key[0] - '1'), true
}

func adjacentTab(current int, key string) int {
	switch key {
	case "[":
		return (current - 1 + len(tabs)) % len(tabs)
	case "]":
		return (current + 1) % len(tabs)
	default:
		return current
	}
}
