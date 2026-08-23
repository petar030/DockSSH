// Package docker defines the external SDK handles owned by application
// bootstrap. It contains no Docker behavior.
package docker

import (
	"github.com/docker/cli/cli/command"
	composeapi "github.com/docker/compose/v5/pkg/api"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// Dependencies groups the SDK objects shared by backend services. Bootstrap
// will create and close Client; individual services only borrow these handles.
type Dependencies struct {
	Client     *client.Client
	DockerCLI  *command.DockerCli
	ComposeAPI composeapi.Compose
}

// EngineEvent is the supported Moby event message used by the future event
// normalizer.
type EngineEvent = events.Message
