// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkspaceBundleUploadAndDownloadRoundTrip(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("workspace"), workspaceBundleChunkSize/len("workspace")+1)
	digest := workspaceBundleDigest(data)
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	upload := &workspaceBundleUploadStream{
		ctx: context.Background(),
		chunks: []*coordinatorv1.WorkspaceBundleChunk{
			{Sequence: 0, Bundle: &coordinatorv1.WorkspaceBundle{Digest: digest, Size: int64(len(data))}, Data: data[:workspaceBundleChunkSize]},
			{Sequence: 1, Data: data[workspaceBundleChunkSize:], IsFinal: true},
		},
	}
	require.NoError(t, handler.PutWorkspaceBundle(upload))
	require.True(t, upload.response.Accepted)

	download := &workspaceBundleDownloadStream{ctx: context.Background()}
	require.NoError(t, handler.GetWorkspaceBundle(&coordinatorv1.GetWorkspaceBundleRequest{Digest: digest}, download))
	require.Len(t, download.chunks, 2)
	assert.Equal(t, byte('w'), download.chunks[0].Data[0])
	assert.Equal(t, data, append(download.chunks[0].Data, download.chunks[1].Data...))
}

func TestPutWorkspaceBundleRejectsDescriptorAfterFirstChunk(t *testing.T) {
	t.Parallel()

	digest := workspaceBundleDigest(nil)
	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	upload := &workspaceBundleUploadStream{
		ctx: context.Background(),
		chunks: []*coordinatorv1.WorkspaceBundleChunk{
			{Sequence: 0, Bundle: &coordinatorv1.WorkspaceBundle{Digest: digest}},
			{Sequence: 1, Bundle: &coordinatorv1.WorkspaceBundle{Digest: digest}},
		},
	}

	err := handler.PutWorkspaceBundle(upload)
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetWorkspaceBundleReportsCorruptStoredBundle(t *testing.T) {
	t.Parallel()

	data := []byte("workspace")
	digest := workspaceBundleDigest(data)
	dir := t.TempDir()
	store := workspacebundle.NewStore(dir, workspacebundle.DefaultLimits())
	require.NoError(t, store.Put(t.Context(), workspacebundle.Descriptor{
		Digest: digest,
		Size:   int64(len(data)),
	}, data))
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	require.NoError(t, os.WriteFile(paths[0], []byte("corrupt"), 0o600))

	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: dir})
	err = handler.GetWorkspaceBundle(
		&coordinatorv1.GetWorkspaceBundleRequest{Digest: digest},
		&workspaceBundleDownloadStream{ctx: context.Background()},
	)
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestGetWorkspaceBundleReportsMissingBundle(t *testing.T) {
	t.Parallel()

	handler := NewHandler(HandlerConfig{WorkspaceBundleDir: t.TempDir()})
	err := handler.GetWorkspaceBundle(
		&coordinatorv1.GetWorkspaceBundleRequest{Digest: workspaceBundleDigest([]byte("missing"))},
		&workspaceBundleDownloadStream{ctx: context.Background()},
	)
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func workspaceBundleDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type workspaceBundleUploadStream struct {
	grpc.ServerStream
	ctx      context.Context
	chunks   []*coordinatorv1.WorkspaceBundleChunk
	index    int
	response *coordinatorv1.PutWorkspaceBundleResponse
}

func (s *workspaceBundleUploadStream) Context() context.Context { return s.ctx }

func (s *workspaceBundleUploadStream) Recv() (*coordinatorv1.WorkspaceBundleChunk, error) {
	if s.index == len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *workspaceBundleUploadStream) SendAndClose(response *coordinatorv1.PutWorkspaceBundleResponse) error {
	s.response = response
	return nil
}

type workspaceBundleDownloadStream struct {
	grpc.ServerStream
	ctx    context.Context
	chunks []*coordinatorv1.WorkspaceBundleChunk
}

func (s *workspaceBundleDownloadStream) Context() context.Context { return s.ctx }

func (s *workspaceBundleDownloadStream) Send(chunk *coordinatorv1.WorkspaceBundleChunk) error {
	s.chunks = append(s.chunks, chunk)
	return nil
}
