//go:build !WITHOUT_DOCKER && (linux || darwin || windows || freebsd || openbsd)

package docker

import (
	"context"
	"fmt"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"github.com/moby/moby/client"
)

func NewDockerNetworkCreateExecutor(ep Endpoint, name string, config *client.NetworkCreateOptions) common.Executor {
	return func(ctx context.Context) error {
		cli := ep.Client()

		// Only create the network if it doesn't exist
		listResult, err := cli.NetworkList(ctx, client.NetworkListOptions{})
		if err != nil {
			return fmt.Errorf("failed to obtain list of networks: %w", err)
		}
		for _, net := range listResult.Items {
			if net.Name == name {
				common.Logger(ctx).Debugf("Network %v exists", name)
				return nil
			}
		}

		_, err = cli.NetworkCreate(ctx, name, *config)
		if err != nil {
			return err
		}

		return nil
	}
}

func NewDockerNetworkRemoveExecutor(ep Endpoint, name string) common.Executor {
	return func(ctx context.Context) error {
		cli := ep.Client()

		// Make sure that all networks of the specified name are removed
		// cli.NetworkRemove refuses to remove a network if there are duplicates
		listResult, err := cli.NetworkList(ctx, client.NetworkListOptions{})
		if err != nil {
			return fmt.Errorf("failed to obtain list of networks: %w", err)
		}
		common.Logger(ctx).Debugf("%v", listResult)
		for _, net := range listResult.Items {
			if net.Name == name {
				inspectResult, err := cli.NetworkInspect(ctx, net.ID, client.NetworkInspectOptions{})
				if err != nil {
					return fmt.Errorf("failed to inspect network %q: %w", net.ID, err)
				}
				if len(inspectResult.Network.Containers) != 0 {
					common.Logger(ctx).Debugf("Refusing to remove network %v because it still has active endpoints", name)
					continue
				}
				if _, err := cli.NetworkRemove(ctx, net.ID, client.NetworkRemoveOptions{}); err != nil {
					return fmt.Errorf("could not remove network %q: %w", net.Name, err)
				}
			}
		}
		return nil
	}
}
