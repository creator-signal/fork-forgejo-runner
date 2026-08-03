//go:build !WITHOUT_DOCKER && (linux || darwin || windows || freebsd || openbsd)

package docker

import (
	"context"
	"slices"

	"code.forgejo.org/forgejo/runner/v13/act/common"
	"github.com/avast/retry-go/v4"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

func NewDockerVolumesRemoveExecutor(ep Endpoint, volumeNames []string) common.Executor {
	return func(ctx context.Context) error {
		cli := ep.Client()

		listResult, err := cli.VolumeList(ctx, client.VolumeListOptions{})
		if err != nil {
			return err
		}

		for _, vol := range listResult.Items {
			if slices.Contains(volumeNames, vol.Name) {
				if err := removeExecutor(ep, vol.Name)(ctx); err != nil {
					return err
				}
			}
		}

		return nil
	}
}

func removeExecutor(ep Endpoint, volume string) common.Executor {
	return func(ctx context.Context) error {
		logger := common.Logger(ctx)
		logger.Debugf("%sdocker volume rm %s", logPrefix, volume)

		if common.Dryrun(ctx) {
			return nil
		}

		return retry.Do(
			func() error {
				removeOpts := client.VolumeRemoveOptions{Force: false}
				if _, err := ep.Client().VolumeRemove(ctx, volume, removeOpts); err != nil {
					if cerrdefs.IsNotFound(err) {
						logger.Debugf("volume %q not found, considering this as a success", volume)
						return nil
					}
					return err
				}
				return nil
			},
			retry.Context(ctx),
			retry.OnRetry(func(n uint, err error) {
				logger.Warnf("failed to remove docker volume %q (retry #%d): %s\n", volume, n, err)
			}),
		)
	}
}
