// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

type workersOnlyServer struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
}

func (workersOnlyServer) GetWorkers(context.Context, *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
	return &coordinatorv1.GetWorkersResponse{}, nil
}

// The engine's coordinator client sends the peer token from its config: the
// configured peer.token, or the token file of a shared data directory.
func TestCoordinatorClientSendsPeerToken(t *testing.T) {
	coordinatorHome := t.TempDir()
	tests := []struct {
		name        string
		home        string
		token       string
		wantRefused bool
	}{
		{name: "shared data directory", home: coordinatorHome},
		{name: "configured peer.token", home: t.TempDir(), token: "shared-token"},
		{name: "no token", home: t.TempDir(), wantRefused: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coordinatorCfg, err := loadConfig(Options{HomeDir: coordinatorHome})
			require.NoError(t, err)
			token := tc.token
			if token == "" {
				token, err = coordinator.LoadOrCreatePeerToken(coordinatorCfg.Core.Peer.TokenFile)
				require.NoError(t, err)
			}
			auth := coordinator.NewPeerTokenAuth(token, tc.token != "", true)
			server := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.UnaryInterceptor()))
			coordinatorv1.RegisterCoordinatorServiceServer(server, workersOnlyServer{})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)

			cfg, err := loadConfig(Options{HomeDir: tc.home})
			require.NoError(t, err)
			cfg.Core.Peer.Token = tc.token
			e := &Engine{cfg: cfg}
			client, err := e.coordinatorClient(DistributedOptions{
				Coordinators: []string{listener.Addr().String()},
				TLS:          TLSOptions{Insecure: true},
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Cleanup(context.Background()) })

			_, err = client.GetWorkers(t.Context())
			if tc.wantRefused {
				assert.Equal(t, codes.Unauthenticated, status.Code(err), "got %v", err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
