//go:build !WITHOUT_DOCKER && (linux || darwin || windows || freebsd || openbsd)

package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"

	"code.forgejo.org/forgejo/runner/v12/act/common"
)

// atomic isn't "really" needed, but its used to avoid the data race detector causing errors.
var cachedSystemPlatform atomic.Pointer[string]

func currentSystemPlatform(ctx context.Context) (string, error) {
	lastCache := cachedSystemPlatform.Load()
	if lastCache != nil {
		return *lastCache, nil
	}

	cli, err := GetDockerClient(ctx)
	if err != nil {
		return "", err
	}
	defer cli.Close()

	info, err := cli.Info(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to get docker info to determine current system architecture: %w", err)
	}

	os := info.OSType
	arch := info.Architecture
	// Bizarrely `docker info` doesn't provide architecture with the same commonly used values for image tagging...
	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}

	systemPlatform := fmt.Sprintf("%s/%s", os, arch)
	cachedSystemPlatform.Store(&systemPlatform)
	return systemPlatform, nil
}

// NewDockerPullExecutor function to create a run executor for the container
func NewDockerPullExecutor(input NewDockerPullExecutorInput) common.Executor {
	return func(ctx context.Context) error {
		logger := common.Logger(ctx)
		logger.Debugf("%sdocker pull %v", logPrefix, input.Image)

		if input.Platform == "" {
			return errors.New("docker pull input.Platform not specified")
		}

		if common.Dryrun(ctx) {
			return nil
		}

		pull := input.ForcePull
		if !pull {
			imageExists, err := ImageExistsLocally(ctx, input.Image, input.Platform)
			logger.Debugf("Image exists? %v", imageExists)
			if err != nil {
				return fmt.Errorf("unable to determine if image already exists for image '%s' (%s): %w", input.Image, input.Platform, err)
			}

			if !imageExists {
				pull = true
			}
		}

		if !pull {
			return nil
		}

		imageRef := cleanImage(ctx, input.Image)
		logger.Debugf("pulling image '%v' (%s)", imageRef, input.Platform)

		cli, err := GetDockerClient(ctx)
		if err != nil {
			return err
		}
		defer cli.Close()

		imagePullOptions, err := getImagePullOptions(ctx, input)
		if err != nil {
			return err
		}

		reader, err := cli.ImagePull(ctx, imageRef, imagePullOptions)

		_ = logDockerResponse(logger, reader, err != nil)
		if err != nil {
			if imagePullOptions.RegistryAuth != "" && strings.Contains(err.Error(), "unauthorized") {
				logger.Errorf("pulling image '%v' (%s) failed with credentials %s retrying without them, please check for stale docker config files", imageRef, input.Platform, err.Error())
				imagePullOptions.RegistryAuth = ""
				reader, err = cli.ImagePull(ctx, imageRef, imagePullOptions)

				_ = logDockerResponse(logger, reader, err != nil)
			}
			return err
		}
		return nil
	}
}

func getImagePullOptions(ctx context.Context, input NewDockerPullExecutorInput) (image.PullOptions, error) {
	imagePullOptions := image.PullOptions{
		Platform: input.Platform,
	}
	logger := common.Logger(ctx)

	if input.Username != "" && input.Password != "" {
		logger.Debugf("using authentication for docker pull")

		authConfig := registry.AuthConfig{
			Username: input.Username,
			Password: input.Password,
		}

		encodedJSON, err := json.Marshal(authConfig)
		if err != nil {
			return imagePullOptions, err
		}

		imagePullOptions.RegistryAuth = base64.URLEncoding.EncodeToString(encodedJSON)
	} else {
		authConfig, err := LoadDockerAuthConfig(ctx, input.Image)
		if err != nil {
			return imagePullOptions, err
		}
		if authConfig.Username == "" && authConfig.Password == "" {
			return imagePullOptions, nil
		}
		logger.Info("using DockerAuthConfig authentication for docker pull")

		encodedJSON, err := json.Marshal(authConfig)
		if err != nil {
			return imagePullOptions, err
		}

		imagePullOptions.RegistryAuth = base64.URLEncoding.EncodeToString(encodedJSON)
	}

	return imagePullOptions, nil
}

func cleanImage(ctx context.Context, image string) string {
	ref, err := reference.ParseAnyReference(image)
	if err != nil {
		common.Logger(ctx).Error(err)
		return ""
	}

	return ref.String()
}
