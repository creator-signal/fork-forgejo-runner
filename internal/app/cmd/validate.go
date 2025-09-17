// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"code.forgejo.org/forgejo/runner/v11/act/model"
	"code.forgejo.org/forgejo/runner/v11/testutils"

	"github.com/spf13/cobra"
)

type validateArgs struct {
	path       string
	repository string
	clonedir   string
	directory  string
	workflow   bool
	action     bool
}

func validate(dir, path string, isWorkflow, isAction bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	defer func() { f.Close() }()

	if isWorkflow {
		_, err = model.ReadWorkflow(f, true)
	} else if isAction {
		_, err = model.ReadAction(f)
	}

	if len(dir) > 0 {
		dir += "/"
	}
	shortPath := strings.TrimPrefix(path, dir)
	kind := "workflow"
	if isAction {
		kind = "action"
	}
	if err != nil {
		fmt.Printf("%s %s schema validation failed:\n%s\n", shortPath, kind, err.Error())
	} else {
		fmt.Printf("%s %s schema validation OK\n", shortPath, kind)
	}

	return nil
}

func validatePath(validateArgs *validateArgs) error {
	if !validateArgs.workflow && !validateArgs.action {
		return errors.New("one of --workflow or --action must be set")
	}
	return validate("", validateArgs.path, validateArgs.workflow, validateArgs.action)
}

func validateHasYamlSuffix(s, suffix string) bool {
	return strings.HasSuffix(s, suffix+".yml") || strings.HasSuffix(s, suffix+".yaml")
}

func validateRepository(validateArgs *validateArgs) error {
	clonedir := validateArgs.clonedir
	if len(clonedir) == 0 {
		tmpdir, err := os.MkdirTemp("", "runner-validate")
		if err != nil {
			return fmt.Errorf("MkdirTemp: %v", err)
		}
		clonedir = filepath.Join(tmpdir, "clonedir")
		defer os.RemoveAll(tmpdir)
	}

	exists, err := testutils.FileExists(clonedir)
	if err != nil {
		return err
	}

	if !exists {
		git := "git"
		args := []string{"clone", "--depth=1", validateArgs.repository, clonedir}
		cmd := exec.Command(git, args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: %s", git, args, output)
			return err
		}
		for _, dir := range []string{".git", ".github", ".gitea"} {
			exists, err := testutils.FileExists(clonedir)
			if err != nil {
				return err
			}
			if exists {
				if err := os.RemoveAll(filepath.Join(clonedir, dir)); err != nil {
					return err
				}
			}
		}
	}

	if err := filepath.Walk(clonedir, func(path string, fi fs.FileInfo, err error) error {
		if validateHasYamlSuffix(path, "/.forgejo/workflows/action") {
			return nil
		}
		isWorkflow := false
		isAction := true
		if validateHasYamlSuffix(path, "/action") {
			if err := validate(clonedir, path, isWorkflow, isAction); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	workflowdir := clonedir + "/.forgejo/workflows"
	exists, err = testutils.FileExists(workflowdir)
	if err != nil {
		return err
	}

	if exists {
		if err := filepath.Walk(workflowdir, func(path string, fi fs.FileInfo, err error) error {
			isWorkflow := true
			isAction := false
			if validateHasYamlSuffix(path, "") {
				if err := validate(clonedir, path, isWorkflow, isAction); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}

func processDirectory(validateArgs *validateArgs) {
	if len(validateArgs.directory) > 0 {
		validateArgs.repository = validateArgs.directory
		validateArgs.clonedir = validateArgs.directory
	}
}

func runValidate(_ context.Context, validateArgs *validateArgs) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		processDirectory(validateArgs)
		if len(validateArgs.path) > 0 {
			return validatePath(validateArgs)
		} else if len(validateArgs.repository) > 0 {
			return validateRepository(validateArgs)
		}
		return nil
	}
}

func loadValidateCmd(ctx context.Context) *cobra.Command {
	validateArgs := validateArgs{}

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate workflows or actions with a schema",
		Long: `
Validate workflows or actions with a schema verifying they are conformant.

The --path argument is a filename that will be validated as a workflow
(if the --workflow flag is set) or as an action (if the --action flag is set).

The --repository argument is a URL to a Git repository that contains
workflows or actions. It will be cloned (in the --clonedir directory
or a temporary location removed when the validation completes).

The --directory argument is the path a repository to be explored for
files to validate.

The following files will be validated when exploring the clone of a repository
(--repository) or a directory (--directory):

- All .forgejo/workflows/*.{yml,yaml} files as workflows
- All **/action.{yml,yaml} files as actions
`,
		Args: cobra.MaximumNArgs(20),
		RunE: runValidate(ctx, &validateArgs),
	}

	validateCmd.Flags().BoolVar(&validateArgs.workflow, "workflow", false, "use the workflow schema")
	validateCmd.Flags().BoolVar(&validateArgs.action, "action", false, "use the action schema")
	validateCmd.MarkFlagsMutuallyExclusive("workflow", "action")

	validateCmd.Flags().StringVar(&validateArgs.clonedir, "clonedir", "", "directory in which the repository will be cloned")
	validateCmd.Flags().StringVar(&validateArgs.repository, "repository", "", "URL to a repository to validate")
	validateCmd.Flags().StringVar(&validateArgs.directory, "directory", "", "directory to a repository to validate")
	validateCmd.Flags().StringVar(&validateArgs.path, "path", "", "path to the file")
	validateCmd.MarkFlagsOneRequired("repository", "path", "directory")
	validateCmd.MarkFlagsMutuallyExclusive("repository", "path", "directory")
	validateCmd.MarkFlagsMutuallyExclusive("directory", "clonedir")

	return validateCmd
}
