// Command ssh-docker-tui starts one shared Docker backend and serves an
// independent Bubble Tea program to each SSH session.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	dockerplatform "github.com/petar030/ssh-native-docker-tui/internal/platform/docker"
	sshplatform "github.com/petar030/ssh-native-docker-tui/internal/platform/ssh"
	"github.com/petar030/ssh-native-docker-tui/internal/serverconfig"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ssh-docker-tui:", err)
		os.Exit(1)
	}
}

func run() error {
	// ----- CLI flags -----
	// Authentication is deliberately not configurable via flags to prevent
	// shell-history leaks.  Use the ssh-docker-tui-config command instead.
	listenAddress := flag.String("listen", "", "SSH listen address (overrides saved config)")
	hostKeyPath := flag.String("host-key", "", "persistent SSH server host-key path (overrides saved config)")
	dockerEndpoint := flag.String("docker-host", "", "optional Docker daemon endpoint; defaults to Docker environment settings")
	dashboardRefresh := flag.Duration("dashboard-refresh", 10*time.Second, "periodic Dashboard base refresh; 0 disables it")
	configPath := flag.String("config", "", "path to server configuration file; defaults to ~/.ssh-docker-tui/config.json")
	var composeRoots []string
	flag.Func("compose-root", "allowed directory for Compose files; may be repeated (overrides saved config)", func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return errors.New("compose-root must not be empty")
		}
		composeRoots = append(composeRoots, value)
		return nil
	})
	flag.Parse()

	if *dashboardRefresh < 0 {
		return errors.New("dashboard-refresh must not be negative")
	}

	// ----- Load persisted configuration -----
	if *configPath == "" {
		path, err := serverconfig.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve config path: %w", err)
		}
		*configPath = path
	}
	cfg, err := serverconfig.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load server configuration: %w", err)
	}

	// ----- Merge explicit non-secret CLI flags over saved values -----
	// Explicit flags take precedence; empty/zero flag values fall back to config.
	effectiveAddress := cfg.Server.Address
	if strings.TrimSpace(*listenAddress) != "" {
		effectiveAddress = *listenAddress
	}
	if effectiveAddress == "" {
		effectiveAddress = sshplatform.DefaultAddress
	}

	effectiveHostKeyPath := cfg.Server.HostKeyPath
	if strings.TrimSpace(*hostKeyPath) != "" {
		effectiveHostKeyPath = *hostKeyPath
	}
	if effectiveHostKeyPath == "" {
		effectiveHostKeyPath = sshplatform.DefaultHostKeyPath
	}

	effectiveDockerEndpoint := cfg.Docker.Endpoint
	if strings.TrimSpace(*dockerEndpoint) != "" {
		effectiveDockerEndpoint = *dockerEndpoint
	}

	effectiveComposeRoots := cfg.Compose.Roots
	if len(composeRoots) > 0 {
		effectiveComposeRoots = composeRoots
	}

	// ----- Start backend and SSH server -----
	processCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	policies := make([]backend.RefreshPolicy, 0, 1)
	if *dashboardRefresh > 0 {
		policies = append(policies, backend.RefreshPolicy{
			Key: backend.RefreshKey{Kind: dashboard.RefreshKindSummary}, Interval: *dashboardRefresh,
		})
	}
	application, err := dockerplatform.NewBackend(processCtx, dockerplatform.BackendConfig{
		Endpoint: effectiveDockerEndpoint, RefreshPolicies: policies, ComposeRoots: effectiveComposeRoots,
	})
	if err != nil {
		return err
	}

	server, err := sshplatform.New(sshplatform.Config{
		Address:     effectiveAddress,
		HostKeyPath: effectiveHostKeyPath,
		Backend:     application,
		Auth:        cfg.Auth,
	})
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return errors.Join(err, application.Close(closeCtx))
	}

	host, port, _ := net.SplitHostPort(server.Address())
	if host == "127.0.0.1" || host == "::1" {
		host = "localhost"
	}
	fmt.Printf("SSH Docker TUI listening on %s\n", server.Address())
	fmt.Printf("Connect with: ssh -p %s %s\n", port, host)
	if cfg.Auth.HasPasswordAuth() {
		fmt.Println("Authentication: password")
	}
	if cfg.Auth.HasKeyAuth() {
		fmt.Printf("Authentication: %d authorized key(s)\n", len(cfg.Auth.AuthorizedKeys))
	}
	if !cfg.Auth.HasAnyAuth() {
		fmt.Println("Authentication: none (loopback only)")
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ListenAndServe() }()

	var serveErr error
	serveFinished := false
	select {
	case <-processCtx.Done():
	case serveErr = <-serveDone:
		serveFinished = true
	}

	serverErr := server.Close()
	if !serveFinished {
		serveErr = <-serveDone
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	backendErr := application.Close(closeCtx)

	return errors.Join(serveErr, serverErr, backendErr)
}
