// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"net"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// workersOnlyServer answers GetWorkers, so a call that passes the peer token
// check succeeds.
type workersOnlyServer struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
}

func (workersOnlyServer) GetWorkers(context.Context, *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
	return &coordinatorv1.GetWorkersResponse{}, nil
}

// With default settings, a coordinator accepts peers that share its data
// directory, as dagu start-all and its workers do, and peers that set its
// peer.token. It refuses a peer on another data directory without a token,
// unless coordinator.require_token is false.
func TestCoordinatorPeerAuthDefaults(t *testing.T) {
	coordinatorHome := t.TempDir()
	tests := []struct {
		name        string
		peerHome    string
		token       string
		optOut      bool
		wantRefused bool
	}{
		{name: "peer sharing the data directory", peerHome: coordinatorHome},
		{name: "remote peer without a token", peerHome: t.TempDir(), wantRefused: true},
		{name: "remote peer with peer.token", peerHome: t.TempDir(), token: "shared-token"},
		{name: "remote peer with require_token false", peerHome: t.TempDir(), optOut: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coordinatorCfg := loadHomeConfig(t, coordinatorHome)
			coordinatorCfg.Core.Peer.Token = tc.token
			if tc.optOut {
				coordinatorCfg.Coordinator.RequireToken = false
			}
			addr := servePeerAuth(t, coordinatorCfg)

			peerCfg := loadHomeConfig(t, tc.peerHome)
			peerCfg.Core.Peer.Token = tc.token
			registry, err := coordinator.NewStaticRegistry([]string{addr})
			require.NoError(t, err)
			client := coordinator.New(registry, coordinator.ConfigFromPeer(peerCfg.Core.Peer))
			t.Cleanup(func() { _ = client.Cleanup(context.Background()) })

			_, err = client.GetWorkers(context.Background())
			if tc.wantRefused {
				assert.Equal(t, codes.Unauthenticated, status.Code(err), "got %v", err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func loadHomeConfig(t *testing.T, home string) *config.Config {
	t.Helper()
	cfg, err := config.NewConfigLoader(viper.New(), config.WithAppHomeDir(home)).Load()
	require.NoError(t, err)
	return cfg
}

// servePeerAuth serves a coordinator gRPC endpoint behind the peer token check
// that cfg sets up, and returns its address.
func servePeerAuth(t *testing.T, cfg *config.Config) string {
	t.Helper()
	auth, err := coordinatorPeerAuth(&Context{Context: context.Background()}, cfg)
	require.NoError(t, err)
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(auth.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(auth.StreamInterceptor()),
	)
	coordinatorv1.RegisterCoordinatorServiceServer(server, workersOnlyServer{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}
