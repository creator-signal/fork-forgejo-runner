package docker

import "github.com/docker/docker/client"

// Endpoint is a connection to a Docker daemon. It owns the API client and the
// per-daemon facts that are invariant for the lifetime of the connection
// (architecture, OS), captured once when the endpoint is dialled.
type Endpoint interface {
	Client() client.APIClient
	Close() error
	RunnerArch() string
	CurrentSystemPlatform() string
}
