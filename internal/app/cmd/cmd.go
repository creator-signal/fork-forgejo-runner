// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"code.forgejo.org/forgejo/runner/v12/internal/app/poll"
	"github.com/spf13/cobra"

	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/ver"
)

func Execute(ctx context.Context) {
	rootCmd := &cobra.Command{
		Use:          "forgejo-runner [event name to run]\nIf no event name passed, will default to \"on: push\"",
		Short:        "Run Forgejo Actions locally by specifying the event name (e.g. `push`) or an action name directly.",
		Args:         cobra.MaximumNArgs(1),
		Version:      ver.Version(),
		SilenceUsage: true,
	}
	configFile := ""
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "Config file path")

	var regArgs registerArgs
	registerCmd := &cobra.Command{
		Use:   "register",
		Short: "Register a runner to the server (deprecated)",
		Args:  cobra.MaximumNArgs(0),
		RunE:  runRegister(ctx, &regArgs, &configFile), // must use a pointer to regArgs
	}
	registerCmd.Flags().BoolVar(&regArgs.NoInteractive, "no-interactive", false, "Disable interactive mode")
	registerCmd.Flags().StringVar(&regArgs.InstanceAddr, "instance", "", "Forgejo instance address")
	registerCmd.Flags().StringVar(&regArgs.Token, "token", "", "Runner token")
	registerCmd.Flags().StringVar(&regArgs.RunnerName, "name", "", "Runner name")
	registerCmd.Flags().StringVar(&regArgs.Labels, "labels", "", "Runner tags, comma separated")
	registerCmd.Flags().BoolVar(&regArgs.Ephemeral, "ephemeral", false, "instruct Forgejo to delete this runner after it has run one job")
	rootCmd.AddCommand(registerCmd)

	rootCmd.AddCommand(createRunnerFileCmd(ctx, &configFile))

	var daemonCmdArgs daemonArgs
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run as daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(ctx, &configFile, &daemonCmdArgs)
		},
	}
	daemonCmd.Flags().StringVar(&daemonCmdArgs.url, "url", "", "URL of the Forgejo instance to connect to")
	daemonCmd.Flags().StringVar(&daemonCmdArgs.uuid, "uuid", "", "UUID of the runner")
	daemonCmd.Flags().StringVar(&daemonCmdArgs.tokenURL, "token-url", "", "URL where the runner token can be loaded from")
	daemonCmd.Flags().StringArrayVar(&daemonCmdArgs.labels, "label", []string{}, "labels of the runner (repeatable)")
	daemonCmd.Flags().DurationVar(&daemonCmdArgs.fetchInterval, "fetch-interval", 0, "specifies the interval duration for fetching resources.")
	rootCmd.AddCommand(daemonCmd)

	var runJobArgs runJobArgs
	jobCmd := &cobra.Command{
		Use:   "one-job",
		Short: "Run only one job",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJob(ctx, &configFile, &runJobArgs)
		},
	}
	jobCmd.Flags().BoolVarP(&runJobArgs.wait, "wait", "w", false, "waits until task has been assigned")
	jobCmd.Flags().StringVar(&runJobArgs.handle, "handle", "", "request the job attempt with the given handle (Forgejo >= 15)")
	jobCmd.Flags().StringVar(&runJobArgs.url, "url", "", "URL of the Forgejo instance to connect to")
	jobCmd.Flags().StringVar(&runJobArgs.uuid, "uuid", "", "UUID of the runner")
	jobCmd.Flags().StringVar(&runJobArgs.tokenURL, "token-url", "", "URL where the runner token can be loaded from")
	jobCmd.Flags().StringArrayVar(&runJobArgs.labels, "label", []string{}, "labels of the runner (repeatable)")
	jobCmd.Flags().DurationVar(&runJobArgs.fetchInterval, "fetch-interval", 0, "specifies the interval duration for fetching resources.")
	rootCmd.AddCommand(jobCmd)

	rootCmd.AddCommand(loadExecCmd(ctx))

	rootCmd.AddCommand(loadValidateCmd(ctx))

	rootCmd.AddCommand(&cobra.Command{
		Use:   "generate-config",
		Short: "Generate an example config file",
		Args:  cobra.MaximumNArgs(0),
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("%s", config.Example) //nolint:forbidigo
		},
	})

	var cacheArgs cacheServerArgs
	cacheCmd := &cobra.Command{
		Use:   "cache-server",
		Short: "Start a cache server for the cache action",
		Args:  cobra.MaximumNArgs(0),
		RunE:  runCacheServer(ctx, &configFile, &cacheArgs),
	}
	cacheCmd.Flags().StringVarP(&cacheArgs.Dir, "dir", "d", "", "Cache directory")
	cacheCmd.Flags().StringVarP(&cacheArgs.Host, "host", "s", "", "Host of the cache server")
	cacheCmd.Flags().Uint16VarP(&cacheArgs.Port, "port", "p", 0, "Port of the cache server")
	cacheCmd.Flags().StringVarP(&cacheArgs.Secret, "secret", "", "", "Shared cache secret")
	rootCmd.AddCommand(cacheCmd)

	// hide completion command
	rootCmd.CompletionOptions.HiddenDefaultCmd = true

	if err := rootCmd.Execute(); err != nil {
		if errors.Is(err, poll.ErrNoTaskReceived) {
			os.Exit(2)
		}

		os.Exit(1)
	}
}
