//go:build !WITHOUT_DOCKER && (linux || darwin || windows || freebsd || openbsd)

package container

import (
	"context"
	"slices"

	"code.forgejo.org/forgejo/runner/v11/act/common"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/volume"
)

func NewDockerVolumesRemoveExecutor(volumeNames []string) common.Executor {
	return func(ctx context.Context) error {
		cli, err := GetDockerClient(ctx)
		if err != nil {
			return err
		}
		defer cli.Close()

		list, err := cli.VolumeList(ctx, volume.ListOptions{Filters: filters.NewArgs()})
		if err != nil {
			return err
		}

		for _, vol := range list.Volumes {
			if slices.Contains(volumeNames, vol.Name) {
				if err := removeExecutor(vol.Name)(ctx); err != nil {
					return err
				}
			}
		}

		return nil
	}
}

func removeExecutor(volume string) common.Executor {
	return func(ctx context.Context) error {
		logger := common.Logger(ctx)
		logger.Debugf("%sdocker volume rm %s", logPrefix, volume)

		if common.Dryrun(ctx) {
			return nil
		}

		cli, err := GetDockerClient(ctx)
		if err != nil {
			return err
		}
		defer cli.Close()

		force := false
		return cli.VolumeRemove(ctx, volume, force)
	}
}
