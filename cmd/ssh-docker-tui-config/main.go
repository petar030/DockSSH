package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
	"github.com/petar030/ssh-native-docker-tui/internal/setup"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ssh-docker-tui-config:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to server configuration file; defaults to ~/.ssh-docker-tui/config.json")
	flag.Parse()

	if *configPath == "" {
		path, err := serverconfig.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve default config path: %w", err)
		}
		*configPath = path
	}

	cfg, err := serverconfig.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	// Create and run the local setup TUI.
	// Store is nil, which instructs the model to use the default direct filesystem store.
	model := setup.New(*configPath, cfg, nil)
	program := tea.NewProgram(model)

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run setup UI: %w", err)
	}

	return nil
}
