// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"

	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	gouuid "github.com/google/uuid"
	"github.com/spf13/cobra"
)

type connectTokenArgs struct {
	InstanceURL string
	Name        string
	UUID        string
	Token       string
	Labels      []string
}

func createConnectTokenCmd(configFile *string) *cobra.Command {
	var arguments connectTokenArgs
	connectTokenCmd := &cobra.Command{
		Use:   "token",
		Short: "Connect using a runner token",
		Args:  cobra.MaximumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			return connectToken(configFile, &arguments)
		},
	}

	connectTokenCmd.Flags().StringVar(&arguments.InstanceURL, "instance", "", "URL of the Forgejo instance to connect to")
	_ = connectTokenCmd.MarkFlagRequired("instance")

	connectTokenCmd.Flags().StringVar(&arguments.Name, "name", "", "The name of the runner in Forgejo")
	_ = connectTokenCmd.MarkFlagRequired("name")

	connectTokenCmd.Flags().StringVar(&arguments.UUID, "uuid", "", "The UUID of the runner")
	_ = connectTokenCmd.MarkFlagRequired("uuid")

	connectTokenCmd.Flags().StringVar(&arguments.Token, "token", "", "The runner's token")
	_ = connectTokenCmd.MarkFlagRequired("token")

	connectTokenCmd.Flags().StringSliceVarP(&arguments.Labels, "label", "", []string{}, "Runner labels (repeated or comma-separated)")

	return connectTokenCmd
}

func connectToken(configFile *string, arguments *connectTokenArgs) error {
	cfg, err := config.New(config.FromFile(*configFile))
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	if arguments.Name == "" {
		return errors.New("required name is empty")
	}
	if _, err := gouuid.Parse(arguments.UUID); err != nil {
		return fmt.Errorf("invalid uuid: %w", err)
	}

	if err := validateSecret(arguments.Token); err != nil {
		return fmt.Errorf("invalid token: %w", err)
	}

	registration := &config.Registration{
		Name:    arguments.Name,
		UUID:    arguments.UUID,
		Token:   arguments.Token,
		Address: arguments.InstanceURL,
		Labels:  arguments.Labels,
	}

	if err := config.SaveRegistration(cfg.Runner.File, registration); err != nil {
		return fmt.Errorf("failed to save runner config to %s: %w", cfg.Runner.File, err)
	}

	return nil
}
