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
	"syscall"
	"time"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
	dockerplatform "github.com/petar030/ssh-native-docker-tui/internal/platform/docker"
	sshplatform "github.com/petar030/ssh-native-docker-tui/internal/platform/ssh"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ssh-docker-tui:", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddress := flag.String("listen", sshplatform.DefaultAddress, "loopback SSH listen address")
	hostKeyPath := flag.String("host-key", sshplatform.DefaultHostKeyPath, "persistent SSH server host-key path")
	dockerEndpoint := flag.String("docker-host", "", "optional Docker daemon endpoint; defaults to Docker environment settings")
	dashboardRefresh := flag.Duration("dashboard-refresh", 10*time.Second, "periodic Dashboard base refresh; 0 disables it")
	flag.Parse()

	if *dashboardRefresh < 0 {
		return errors.New("dashboard-refresh must not be negative")
	}

	processCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	policies := make([]backend.RefreshPolicy, 0, 1)
	if *dashboardRefresh > 0 {
		policies = append(policies, backend.RefreshPolicy{
			Key: backend.RefreshKey{Kind: dashboard.RefreshKindSummary}, Interval: *dashboardRefresh,
		})
	}
	application, err := dockerplatform.NewBackend(processCtx, dockerplatform.BackendConfig{
		Endpoint: *dockerEndpoint, RefreshPolicies: policies,
	})
	if err != nil {
		return err
	}

	server, err := sshplatform.New(sshplatform.Config{
		Address: *listenAddress, HostKeyPath: *hostKeyPath, Backend: application,
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
