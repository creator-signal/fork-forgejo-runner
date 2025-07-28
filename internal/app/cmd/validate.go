// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"code.forgejo.org/forgejo/runner/act/model"

	"github.com/spf13/cobra"
)

type validateArgs struct {
	path     string
	workflow bool
	action   bool
}

func validate(in io.Reader, validateArgs *validateArgs) {
	var err error
	if validateArgs.workflow {
		_, err = model.ReadWorkflow(in, true)
	} else if validateArgs.action {
		_, err = model.ReadAction(in)
	}

	if err != nil {
		fmt.Printf("schema validation failed:\n%s\n", err.Error())
	} else {
		fmt.Println("schema validation OK")
	}
}

func runValidate(_ context.Context, validateArgs *validateArgs) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		f, err := os.Open(validateArgs.path)
		if err != nil {
			return fmt.Errorf("--path %q: %v", validateArgs.path, err)
		}
		defer func() { f.Close() }()
		if !validateArgs.workflow && !validateArgs.action {
			return errors.New("one of --workflow or --action must be set")
		}
		validate(f, validateArgs)
		return nil
	}
}

func loadValidateCmd(ctx context.Context) *cobra.Command {
	validateArgs := validateArgs{}

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate workflows or actions with a schema",
		Args:  cobra.MaximumNArgs(20),
		RunE:  runValidate(ctx, &validateArgs),
	}

	validateCmd.Flags().BoolVarP(&validateArgs.workflow, "workflow", "", false, "use the workflow schema")
	validateCmd.Flags().BoolVarP(&validateArgs.action, "action", "", false, "use the action schema")
	validateCmd.MarkFlagsMutuallyExclusive("workflow", "action")
	validateCmd.Flags().StringVarP(&validateArgs.path, "path", "", "", "path to the file")

	return validateCmd
}
