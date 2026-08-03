package docker

import (
	"io"
	"os"
	"testing"

	"code.forgejo.org/forgejo/runner/v13/testutils"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func init() {
	log.SetLevel(log.DebugLevel)
}

func TestImageExistsLocally(t *testing.T) {
	testutils.RequireTestFeatures(t, testutils.TestFeatureDocker)
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := t.Context()
	// to help make this test reliable and not flaky, we need to have
	// an image that will exist, and one that won't exist

	ep, err := NewEndpoint(ctx, os.Getenv("DOCKER_HOST"))
	assert.Nil(t, err)
	defer ep.Close()
	cli := ep.Client()

	// Test if image exists with specific tag
	invalidImageTag, err := ImageExistsLocally(ctx, ep, "code.forgejo.org/oci/alpine:this-random-tag-will-never-exist", "linux/amd64")
	assert.Nil(t, err)
	assert.Equal(t, false, invalidImageTag)

	// Test if image exists with specific architecture (image platform)
	invalidImagePlatform, err := ImageExistsLocally(ctx, ep, "code.forgejo.org/oci/alpine:latest", "windows/amd64")
	assert.Nil(t, err)
	assert.Equal(t, false, invalidImagePlatform)

	// Chose alpine latest because it's so small
	// maybe we should build an image instead so that tests aren't reliable on dockerhub
	readerDefault, err := cli.ImagePull(ctx, "code.forgejo.org/oci/alpine:latest", client.ImagePullOptions{
		Platforms: []ocispec.Platform{{OS: "linux", Architecture: "amd64"}},
	})
	assert.Nil(t, err)
	defer readerDefault.Close()
	_, err = io.ReadAll(readerDefault)
	assert.Nil(t, err)

	imageDefaultArchExists, err := ImageExistsLocally(ctx, ep, "code.forgejo.org/oci/alpine:latest", "linux/amd64")
	assert.Nil(t, err)
	assert.Equal(t, true, imageDefaultArchExists)

	// Validate if another architecture platform can be pulled
	readerArm64, err := cli.ImagePull(ctx, "code.forgejo.org/oci/alpine:latest", client.ImagePullOptions{
		Platforms: []ocispec.Platform{{OS: "linux", Architecture: "arm64"}},
	})
	assert.Nil(t, err)
	defer readerArm64.Close()
	_, err = io.ReadAll(readerArm64)
	assert.Nil(t, err)

	imageArm64Exists, err := ImageExistsLocally(ctx, ep, "code.forgejo.org/oci/alpine:latest", "linux/arm64")
	assert.Nil(t, err)
	assert.Equal(t, true, imageArm64Exists)
}
