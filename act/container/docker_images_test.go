package container

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/moby/go-archive"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/skip"
)

func init() {
	log.SetLevel(log.DebugLevel)
}

func TestImageExistsLocally(t *testing.T) {
	skip.If(t, runtime.GOOS != "linux") // Windows and macOS cannot natively run Linux containers
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := t.Context()
	// to help make this test reliable and not flaky, we need to have
	// an image that will exist, and one that won't exist

	// Test if image exists with specific tag
	invalidImageTag, err := ImageExistsLocally(ctx, "code.forgejo.org/oci/alpine:this-random-tag-will-never-exist", "linux/amd64")
	assert.Nil(t, err)
	assert.Equal(t, false, invalidImageTag)

	// Test if image exists with specific architecture (image platform)
	invalidImagePlatform, err := ImageExistsLocally(ctx, "code.forgejo.org/oci/alpine:latest", "windows/amd64")
	assert.Nil(t, err)
	assert.Equal(t, false, invalidImagePlatform)

	// pull an image
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	assert.Nil(t, err)

	// Chose alpine latest because it's so small
	// maybe we should build an image instead so that tests aren't reliable on dockerhub
	readerDefault, err := cli.ImagePull(ctx, "code.forgejo.org/oci/alpine:latest", image.PullOptions{
		Platform: "linux/amd64",
	})
	assert.Nil(t, err)
	defer readerDefault.Close()
	_, err = io.ReadAll(readerDefault)
	assert.Nil(t, err)

	imageDefaultArchExists, err := ImageExistsLocally(ctx, "code.forgejo.org/oci/alpine:latest", "linux/amd64")
	assert.Nil(t, err)
	assert.Equal(t, true, imageDefaultArchExists)

	// Validate if another architecture platform can be pulled
	readerArm64, err := cli.ImagePull(ctx, "code.forgejo.org/oci/alpine:latest", image.PullOptions{
		Platform: "linux/arm64",
	})
	assert.Nil(t, err)
	defer readerArm64.Close()
	_, err = io.ReadAll(readerArm64)
	assert.Nil(t, err)

	imageArm64Exists, err := ImageExistsLocally(ctx, "code.forgejo.org/oci/alpine:latest", "linux/arm64")
	assert.Nil(t, err)
	assert.Equal(t, true, imageArm64Exists)
}

func TestUseImageEntrypoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	skip.If(t, runtime.GOOS != "linux")

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	assert.Nil(t, err)

	platform, err := CurrentSystemPlatform(t.Context())
	require.NoError(t, err)

	t.Run("Error if image does not exist", func(t *testing.T) {
		res, err := UseImageEntrypoint(t.Context(), "data.forgejo.org/oci/alpine:this-random-tag-will-never-exist", platform)
		assert.False(t, res)
		assert.ErrorContains(t, err, "failed to inspect image")
	})

	t.Run("False if org.forgejo.runner.use-image-entrypoint is absent", func(t *testing.T) {
		imageName := "code.forgejo.org/oci/alpine:latest"

		// The image cannot be inspected if it isn't available locally.
		readerDefault, err := cli.ImagePull(t.Context(), imageName, image.PullOptions{Platform: platform})
		assert.Nil(t, err)
		defer readerDefault.Close()
		_, err = io.ReadAll(readerDefault)
		assert.Nil(t, err)

		res, err := UseImageEntrypoint(t.Context(), imageName, platform)
		assert.False(t, res)
		assert.Nil(t, err)
	})

	t.Run("True if org.forgejo.runner.use-image-entrypoint is truthy", func(t *testing.T) {
		// Include a random string in the image name to prevent the test from accidentally removing an identically named
		// image on the developer's machine.
		imageTag := fmt.Sprintf("forgejo-runner-test-%s:latest", strings.ToLower(rand.Text()))

		tempDir := t.TempDir()
		err = os.WriteFile(filepath.Join(tempDir, "Dockerfile"), []byte("FROM data.forgejo.org/oci/alpine:latest"), 0o644)
		require.NoError(t, err)

		contextReader, err := archive.TarWithOptions(tempDir, &archive.TarOptions{})
		require.NoError(t, err)
		defer contextReader.Close()

		defer func() {
			_, err := cli.ImageRemove(t.Context(), imageTag, image.RemoveOptions{})
			require.NoError(t, err)
		}()

		options := build.ImageBuildOptions{
			Context:  contextReader,
			Platform: platform,
			Tags:     []string{imageTag},
			Labels:   map[string]string{"org.forgejo.runner.use-image-entrypoint": "T"},
			Remove:   true,
		}
		response, err := cli.ImageBuild(t.Context(), contextReader, options)
		require.NoError(t, err)

		_, err = io.ReadAll(response.Body)
		require.NoError(t, err)

		res, err := UseImageEntrypoint(t.Context(), imageTag, platform)
		assert.True(t, res)
		assert.Nil(t, err)
	})

	t.Run("False if org.forgejo.runner.use-image-entrypoint is falsy", func(t *testing.T) {
		// Include a random string in the image name to prevent the test from accidentally removing an identically named
		// image on the developer's machine.
		imageTag := fmt.Sprintf("forgejo-runner-test-%s:latest", strings.ToLower(rand.Text()))

		tempDir := t.TempDir()
		err = os.WriteFile(filepath.Join(tempDir, "Dockerfile"), []byte("FROM data.forgejo.org/oci/alpine:latest"), 0o644)
		require.NoError(t, err)

		contextReader, err := archive.TarWithOptions(tempDir, &archive.TarOptions{})
		require.NoError(t, err)
		defer contextReader.Close()

		defer func() {
			_, err := cli.ImageRemove(t.Context(), imageTag, image.RemoveOptions{})
			require.NoError(t, err)
		}()

		options := build.ImageBuildOptions{
			Context:  contextReader,
			Platform: platform,
			Tags:     []string{imageTag},
			Labels:   map[string]string{"org.forgejo.runner.use-image-entrypoint": "F"},
			Remove:   true,
		}
		response, err := cli.ImageBuild(t.Context(), contextReader, options)
		require.NoError(t, err)

		_, err = io.ReadAll(response.Body)
		require.NoError(t, err)

		res, err := UseImageEntrypoint(t.Context(), imageTag, platform)
		assert.False(t, res)
		assert.Nil(t, err)
	})
}

func TestParsePlatform(t *testing.T) {
	tests := []struct {
		input  string
		output v1.Platform
	}{
		{
			input: "linux/amd64",
			output: v1.Platform{
				Architecture: "amd64",
				OS:           "linux",
			},
		},
	}
	for _, tc := range tests {
		plat, err := parsePlatform(tc.input)
		require.NoError(t, err)
		assert.Equal(t, tc.output.Architecture, plat.Architecture)
		assert.Equal(t, tc.output.OS, plat.OS)
	}
}
