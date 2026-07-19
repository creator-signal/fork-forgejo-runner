//go:build !WITHOUT_DOCKER && (linux || darwin || windows || freebsd || openbsd)

package docker

import (
	"context"
	"fmt"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// ImageExistsLocally returns a boolean indicating if an image with the
// requested name, tag and architecture exists in the local docker image store
func ImageExistsLocally(ctx context.Context, ep Endpoint, imageName, platform string) (bool, error) {
	logger := common.Logger(ctx)

	cli := ep.Client()

	if supportsImageInspectPlatform(ctx, cli) {
		platSpec, err := parsePlatform(platform)
		if err != nil {
			return false, err
		}

		if _, err := cli.ImageInspect(ctx, imageName, client.ImageInspectWithPlatform(&platSpec)); err != nil {
			if cerrdefs.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to inspect image %q: %w", imageName, err)
		}
		return true, nil
	}

	inspectImage, err := cli.ImageInspect(ctx, imageName)
	if cerrdefs.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to inspect image %q: %w", imageName, err)
	}

	imagePlatform := fmt.Sprintf("%s/%s", inspectImage.Os, inspectImage.Architecture)
	if platform == "" || platform == "any" || imagePlatform == platform {
		return true, nil
	}

	logger.Infof("Docker daemon does not support image platform inspection (API 1.49+ required); "+
		"the image's platform %q is not the expected: %q", imagePlatform, platform)

	return false, nil
}
