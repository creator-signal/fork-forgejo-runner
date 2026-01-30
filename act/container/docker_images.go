//go:build !WITHOUT_DOCKER && (linux || darwin || windows || freebsd || openbsd)

package container

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

const LabelUseImageEntrypoint = "org.forgejo.runner.use-image-entrypoint"

func parsePlatform(platform string) (*v1.Platform, error) {
	desiredPlatform := strings.SplitN(platform, `/`, 2)
	if len(desiredPlatform) != 2 {
		return nil, fmt.Errorf("incorrect container platform option '%s'", platform)
	}
	platSpecs := &v1.Platform{
		Architecture: desiredPlatform[1],
		OS:           desiredPlatform[0],
	}
	return platSpecs, nil
}

// ImageExistsLocally returns a boolean indicating if an image with the
// requested name, tag and architecture exists in the local docker image store
func ImageExistsLocally(ctx context.Context, imageName, platform string) (bool, error) {
	logger := common.Logger(ctx)

	cli, err := GetDockerClient(ctx)
	if err != nil {
		return false, err
	}
	defer cli.Close()

	if supportsImageInspectPlatform(ctx, cli) {
		platSpecs, err := parsePlatform(platform)
		if err != nil {
			return false, err
		}

		_, err = cli.ImageInspect(ctx, imageName, client.ImageInspectWithPlatform(platSpecs))
		if cerrdefs.IsNotFound(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}

		return true, nil
	}

	inspectImage, err := cli.ImageInspect(ctx, imageName)
	if cerrdefs.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}

	imagePlatform := fmt.Sprintf("%s/%s", inspectImage.Os, inspectImage.Architecture)
	if platform == "" || platform == "any" || imagePlatform == platform {
		return true, nil
	}

	logger.Infof("Docker daemon does not support image platform inspection (API 1.49+ required) -- runner found an image but platform does not match expected: %s (image) != %s (platform)", imagePlatform, platform)

	return false, nil
}

// UseImageEntrypoint returns true if the label named LabelUseImageEntrypoint is attached to the container image to
// start and its value evaluates to boolean true. It returns false in all other conditions.
//
// If UseImageEntrypoint returns true, then a container that is started with the given image should use the image's
// default entrypoint and not override it.
func UseImageEntrypoint(ctx context.Context, imageName, platform string) (bool, error) {
	cli, err := GetDockerClient(ctx)
	if err != nil {
		return false, err
	}
	defer cli.Close()

	var inspectOptions []client.ImageInspectOption
	if supportsImageInspectPlatform(ctx, cli) {
		dockerPlatform, err := parsePlatform(platform)
		if err != nil {
			return false, err
		}
		inspectOptions = append(inspectOptions, client.ImageInspectWithPlatform(dockerPlatform))
	}

	inspectResponse, err := cli.ImageInspect(ctx, imageName, inspectOptions...)
	if err != nil {
		return false, fmt.Errorf("failed to inspect image %q for platform %q: %w", imageName, platform, err)
	}
	if value, ok := inspectResponse.Config.Labels[LabelUseImageEntrypoint]; ok {
		result, err := strconv.ParseBool(value)
		if err != nil {
			return false, fmt.Errorf("cannot parse label %q of image %q: %w", LabelUseImageEntrypoint, imageName, err)
		}
		return result, nil
	}

	return false, nil
}

// RemoveImage removes image from local store, the function is used to run different
// container image architectures
func RemoveImage(ctx context.Context, imageName string, force, pruneChildren bool) (bool, error) {
	cli, err := GetDockerClient(ctx)
	if err != nil {
		return false, err
	}
	defer cli.Close()

	inspectImage, err := cli.ImageInspect(ctx, imageName)
	if cerrdefs.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}

	if _, err = cli.ImageRemove(ctx, inspectImage.ID, image.RemoveOptions{
		Force:         force,
		PruneChildren: pruneChildren,
	}); err != nil {
		return false, err
	}

	return true, nil
}
