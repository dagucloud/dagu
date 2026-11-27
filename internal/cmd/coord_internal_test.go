// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"net"
	"path/filepath"
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
// unless coordinator.require_token is false. A coordinator that verifies
// client certificates accepts a peer with a verified certificate and no token,
// unless coordinator.require_token is set to true.
func TestCoordinatorPeerAuthDefaults(t *testing.T) {
	coordinatorHome := t.TempDir()
	tests := []struct {
		name         string
		peerHome     string
		token        string
		requireToken *bool
		mutualTLS    bool
		wantRefused  bool
	}{
		{name: "peer sharing the data directory", peerHome: coordinatorHome},
		{name: "remote peer without a token", peerHome: t.TempDir(), wantRefused: true},
		{name: "remote peer with peer.token", peerHome: t.TempDir(), token: "shared-token"},
		{name: "remote peer with require_token false", peerHome: t.TempDir(), requireToken: new(false)},
		{name: "verified client certificate without a token", peerHome: t.TempDir(), mutualTLS: true},
		{name: "verified client certificate with require_token true", peerHome: t.TempDir(), mutualTLS: true, requireToken: new(true), wantRefused: true},
		{name: "verified client certificate and peer.token with require_token true", peerHome: t.TempDir(), mutualTLS: true, requireToken: new(true), token: "shared-token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coordinatorCfg := loadHomeConfig(t, coordinatorHome)
			coordinatorCfg.Core.Peer.Token = tc.token
			coordinatorCfg.Coordinator.RequireToken = tc.requireToken
			peerCfg := loadHomeConfig(t, tc.peerHome)
			peerCfg.Core.Peer.Token = tc.token
			if tc.mutualTLS {
				useMutualTLS(coordinatorCfg, peerCfg)
			}
			addr := servePeerAuth(t, coordinatorCfg)

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

// useMutualTLS makes the coordinator verify client certificates against the
// test CA and gives the peer a certificate that CA signed. The test
// certificate names no host, so the peer skips the server name check.
func useMutualTLS(coordinatorCfg, peerCfg *config.Config) {
	certs := filepath.Join("..", "testdata", "certs")
	coordinatorCfg.Core.Peer.Insecure = false
	coordinatorCfg.Core.Peer.CertFile = filepath.Join(certs, "cert.pem")
	coordinatorCfg.Core.Peer.KeyFile = filepath.Join(certs, "key.pem")
	coordinatorCfg.Core.Peer.ClientCaFile = filepath.Join(certs, "ca.pem")
	peerCfg.Core.Peer.Insecure = false
	peerCfg.Core.Peer.CertFile = filepath.Join(certs, "cert.pem")
	peerCfg.Core.Peer.KeyFile = filepath.Join(certs, "key.pem")
	peerCfg.Core.Peer.SkipTLSVerify = true
}

func loadHomeConfig(t *testing.T, home string) *config.Config {
	t.Helper()
	cfg, err := config.NewConfigLoader(viper.New(), config.WithAppHomeDir(home)).Load()
	require.NoError(t, err)
	return cfg
}

// servePeerAuth serves a coordinator gRPC endpoint behind the peer token check
// and TLS that cfg sets up, and returns its address.
func servePeerAuth(t *testing.T, cfg *config.Config) string {
	t.Helper()
	auth, err := coordinatorPeerAuth(&Context{Context: context.Background()}, cfg)
	require.NoError(t, err)
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(auth.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(auth.StreamInterceptor()),
	}
	if peer := cfg.Core.Peer; peer.CertFile != "" {
		creds, err := loadCoordinatorTLSCredentials(&config.TLSConfig{CertFile: peer.CertFile, KeyFile: peer.KeyFile, CAFile: peer.ClientCaFile})
		require.NoError(t, err)
		opts = append(opts, grpc.Creds(creds))
	}
	server := grpc.NewServer(opts...)
	coordinatorv1.RegisterCoordinatorServiceServer(server, workersOnlyServer{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}
