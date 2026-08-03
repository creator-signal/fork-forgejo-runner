//go:build !WITHOUT_DOCKER && (linux || darwin || windows || freebsd || openbsd)

package docker

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/moby/moby/client"

	"github.com/moby/go-archive"
	"github.com/moby/go-archive/compression"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"code.forgejo.org/forgejo/runner/v13/act/common"
)

// NewDockerBuildExecutor function to create a run executor for the container
func NewDockerBuildExecutor(ep Endpoint, input NewDockerBuildExecutorInput) common.Executor {
	return func(ctx context.Context) error {
		logger := common.Logger(ctx)
		if input.Platform != "" {
			logger.Infof("%sdocker build -t %s --platform %s %s", logPrefix, input.ImageTag, input.Platform, input.ContextDir)
		} else {
			logger.Infof("%sdocker build -t %s %s", logPrefix, input.ImageTag, input.ContextDir)
		}
		if common.Dryrun(ctx) {
			return nil
		}

		cli := ep.Client()

		logger.Debugf("Building image from '%v'", input.ContextDir)

		options := client.ImageBuildOptions{
			Tags:        []string{input.ImageTag},
			Remove:      true,
			Platforms:   []ocispec.Platform{},
			AuthConfigs: LoadDockerAuthConfigs(ctx),
			Dockerfile:  input.Dockerfile,
		}
		if input.Platform != "" {
			platSpec, err := parsePlatform(input.Platform)
			if err != nil {
				return err
			}
			options.Platforms = append(options.Platforms, platSpec)
		}

		var (
			buildContext io.ReadCloser
			err          error
		)
		if input.BuildContext != nil {
			buildContext = io.NopCloser(input.BuildContext)
		} else {
			buildContext, err = createBuildContext(ctx, input.ContextDir, input.Dockerfile)
		}
		if err != nil {
			return err
		}

		defer buildContext.Close()

		logger.Debugf("Creating image from context dir %q with tag %q and platform %q",
			input.ContextDir, input.ImageTag, input.Platform)

		resp, err := cli.ImageBuild(ctx, buildContext, options)

		err = logDockerResponse(logger, resp.Body, err != nil)
		if err != nil {
			return err
		}
		return nil
	}
}

func createBuildContext(ctx context.Context, contextDir, relDockerfile string) (io.ReadCloser, error) {
	common.Logger(ctx).Debugf("Creating archive for build context dir '%s' with relative dockerfile '%s'", contextDir, relDockerfile)

	// And canonicalize dockerfile name to a platform-independent one
	relDockerfile = filepath.ToSlash(relDockerfile)

	f, err := os.Open(filepath.Join(contextDir, ".dockerignore"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	defer f.Close()

	var excludes []string
	if err == nil {
		excludes, err = ignorefile.ReadAll(f)
		if err != nil {
			return nil, err
		}
	}

	// If .dockerignore mentions .dockerignore or the Dockerfile
	// then make sure we send both files over to the daemon
	// because Dockerfile is, obviously, needed no matter what, and
	// .dockerignore is needed to know if either one needs to be
	// removed. The daemon will remove them for us, if needed, after it
	// parses the Dockerfile. Ignore errors here, as they will have been
	// caught by validateContextDirectory above.
	includes := []string{"."}
	keepThem1, _ := patternmatcher.Matches(".dockerignore", excludes)
	keepThem2, _ := patternmatcher.Matches(relDockerfile, excludes)
	if keepThem1 || keepThem2 {
		includes = append(includes, ".dockerignore", relDockerfile)
	}

	compression := compression.None
	buildCtx, err := archive.TarWithOptions(contextDir, &archive.TarOptions{
		Compression:     compression,
		ExcludePatterns: excludes,
		IncludeFiles:    includes,
	})
	if err != nil {
		return nil, err
	}

	return buildCtx, nil
}
