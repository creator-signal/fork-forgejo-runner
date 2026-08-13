// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	pluginv1alpha "code.forgejo.org/forgejo/runner/v13/act/plugin/proto/v1alpha"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type healthOnlyServer struct {
	pluginv1alpha.UnimplementedBackendPluginServer
	caps *pluginv1alpha.CapabilitiesResponse
}

func (h *healthOnlyServer) Capabilities(_ context.Context, _ *pluginv1alpha.CapabilitiesRequest) (*pluginv1alpha.CapabilitiesResponse, error) {
	return h.caps, nil
}

func startListener(t *testing.T, srv *grpc.Server, healthStatus grpc_health_v1.HealthCheckResponse_ServingStatus) net.Listener {
	t.Helper()
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, healthSrv)
	healthSrv.SetServingStatus("", healthStatus)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis
}

func TestNewClient_RejectsPlainTCPByDefault(t *testing.T) {
	_, err := NewClient(t.Context(), "127.0.0.1:1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TCP address requires WithTLS or WithAllowPlainTCP")
}

func TestNewClient_AcceptsTCPWithAllowPlainTCP(t *testing.T) {
	srv := grpc.NewServer()
	pluginv1alpha.RegisterBackendPluginServer(srv, &healthOnlyServer{
		caps: &pluginv1alpha.CapabilitiesResponse{Name: "tcp"},
	})
	lis := startListener(t, srv, grpc_health_v1.HealthCheckResponse_SERVING)

	c, err := NewClient(t.Context(), lis.Addr().String(), WithAllowPlainTCP())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	assert.Equal(t, "tcp", c.Capabilities().GetName())
}

func TestNewClient_AcceptsTLSConfig(t *testing.T) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	creds, err := transportCredentials(false, &clientOptions{tlsConfig: cfg})
	require.NoError(t, err)
	assert.NotNil(t, creds)
}

func TestNewClient_RejectsNotServingHealth(t *testing.T) {
	srv := grpc.NewServer()
	pluginv1alpha.RegisterBackendPluginServer(srv, &healthOnlyServer{
		caps: &pluginv1alpha.CapabilitiesResponse{Name: "x"},
	})
	lis := startListener(t, srv, grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	_, err := NewClient(t.Context(), lis.Addr().String(), WithAllowPlainTCP())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status NOT_SERVING")
}

func TestNewClient_RejectsIncompleteCapabilities(t *testing.T) {
	srv := grpc.NewServer()
	pluginv1alpha.RegisterBackendPluginServer(srv, &healthOnlyServer{
		caps: &pluginv1alpha.CapabilitiesResponse{Name: ""},
	})
	lis := startListener(t, srv, grpc_health_v1.HealthCheckResponse_SERVING)

	_, err := NewClient(t.Context(), lis.Addr().String(), WithAllowPlainTCP())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required fields")
	assert.Contains(t, err.Error(), "name")
}
