package images

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/distribution/reference"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/petar030/ssh-native-docker-tui/internal/backend"
	"github.com/petar030/ssh-native-docker-tui/internal/backend/dashboard"
)

func (api *API) pullJob(value string, options PullOptions) (backend.JobRequest, error) {
	imageReference, err := normalizePullReference(value)
	if err != nil {
		return backend.JobRequest{}, err
	}
	platform, err := parsePlatform(options.Platform)
	if err != nil {
		return backend.JobRequest{}, err
	}
	pullOptions := client.ImagePullOptions{}
	if platform != nil {
		pullOptions.Platforms = []ocispec.Platform{*platform}
	}
	return backend.JobRequest{
		Page: backend.PageImages, OperationID: "image.pull", Operation: "pull image",
		ConflictKey: "image.pull:" + imageReference,
		Affected:    []backend.AffectedResource{{Kind: "image", ID: imageReference}},
		RefreshKeys: []backend.RefreshKey{{Kind: RefreshKindList}, {Kind: dashboard.RefreshKindSummary}},
		Run: func(ctx context.Context, report func(backend.ProgressEvent)) error {
			response, err := api.docker.ImagePull(ctx, imageReference, pullOptions)
			if err != nil {
				return classifyDockerError("pull image", imageReference, err)
			}
			defer response.Close()
			for message, streamErr := range response.JSONMessages(ctx) {
				if streamErr != nil {
					return classifyDockerError("pull image", imageReference, streamErr)
				}
				if message.Error != nil {
					return classifyDockerError("pull image", imageReference, errors.New(message.Error.Message))
				}
				status := strings.TrimSpace(message.Status)
				text := strings.TrimSpace(message.Stream)
				if status == "" && text == "" {
					continue
				}
				progress := backend.ProgressEvent{Status: status, Resource: message.ID, Message: text}
				if progress.Resource == "" {
					progress.Resource = imageReference
				}
				if progress.Status == "" {
					progress.Status = "running"
				}
				if message.Progress != nil {
					progress.Current = message.Progress.Current
					progress.Total = message.Progress.Total
				}
				report(progress)
			}
			return classifyDockerError("pull image", imageReference, ctx.Err())
		},
	}, nil
}

func normalizePullReference(value string) (string, error) {
	value = strings.TrimSpace(value)
	named, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return "", &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "pull image", Resource: "image reference", ID: value, Err: err}
	}
	return reference.FamiliarString(reference.TagNameOnly(named)), nil
}

func parsePlatform(value string) (*ocispec.Platform, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "pull image", Resource: "platform", ID: value, Err: fmt.Errorf("expected os/architecture[/variant]")}
	}
	platform := &ocispec.Platform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		if parts[2] == "" {
			return nil, &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "pull image", Resource: "platform", ID: value}
		}
		platform.Variant = parts[2]
	}
	return platform, nil
}
