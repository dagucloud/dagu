// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const workspaceBundleChunkSize = 1 << 20

func (h *Handler) PutWorkspaceBundle(stream coordinatorv1.CoordinatorService_PutWorkspaceBundleServer) error {
	if h.workspaceBundleStore == nil {
		return status.Error(codes.FailedPrecondition, "workspace bundle store is not configured")
	}

	first, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return stream.SendAndClose(&coordinatorv1.PutWorkspaceBundleResponse{
			Accepted: false,
			Error:    "workspace bundle descriptor is required",
		})
	}
	if err != nil {
		return status.Error(codes.Internal, "failed to receive workspace bundle: "+err.Error())
	}
	if first == nil || first.Sequence != 0 {
		return status.Error(codes.InvalidArgument, "workspace bundle upload must start at sequence 0")
	}
	if first.Bundle == nil {
		return status.Error(codes.InvalidArgument, "workspace bundle descriptor is required")
	}

	reader := &workspaceBundleUploadReader{
		stream:   stream,
		pending:  first.Data,
		sequence: 1,
		read:     int64(len(first.Data)),
		max:      workspacebundle.DefaultMaxCompressedSize,
	}
	if reader.read > reader.max {
		return status.Error(codes.InvalidArgument, fmt.Sprintf("workspace bundle exceeds compressed size limit %d", reader.max))
	}
	if err := h.workspaceBundleStore.PutReader(stream.Context(), descriptorFromProto(first.Bundle), reader); err != nil {
		if status.Code(err) != codes.Unknown {
			return err
		}
		return stream.SendAndClose(&coordinatorv1.PutWorkspaceBundleResponse{
			Accepted: false,
			Error:    err.Error(),
		})
	}
	return stream.SendAndClose(&coordinatorv1.PutWorkspaceBundleResponse{Accepted: true})
}

type workspaceBundleUploadReader struct {
	stream   coordinatorv1.CoordinatorService_PutWorkspaceBundleServer
	pending  []byte
	sequence uint64
	read     int64
	max      int64
}

func (r *workspaceBundleUploadReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		chunk, err := r.stream.Recv()
		if err != nil {
			return 0, err
		}
		if chunk == nil {
			continue
		}
		if chunk.Sequence != r.sequence {
			return 0, status.Error(codes.InvalidArgument, fmt.Sprintf("workspace bundle sequence mismatch: got %d, want %d", chunk.Sequence, r.sequence))
		}
		r.sequence++
		if chunk.Bundle != nil {
			return 0, status.Error(codes.InvalidArgument, "workspace bundle descriptor is only allowed in the first chunk")
		}
		r.read += int64(len(chunk.Data))
		if r.read > r.max {
			return 0, status.Error(codes.InvalidArgument, fmt.Sprintf("workspace bundle exceeds compressed size limit %d", r.max))
		}
		r.pending = chunk.Data
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (h *Handler) HasWorkspaceBundle(ctx context.Context, req *coordinatorv1.HasWorkspaceBundleRequest) (*coordinatorv1.HasWorkspaceBundleResponse, error) {
	if h.workspaceBundleStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "workspace bundle store is not configured")
	}
	if req == nil || !workspacebundle.ValidDigest(req.Digest) {
		return nil, status.Error(codes.InvalidArgument, "valid workspace bundle digest is required")
	}
	exists, err := h.workspaceBundleStore.Touch(ctx, req.Digest)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to refresh workspace bundle: "+err.Error())
	}
	return &coordinatorv1.HasWorkspaceBundleResponse{
		Exists: exists,
	}, nil
}

func (h *Handler) GetWorkspaceBundle(req *coordinatorv1.GetWorkspaceBundleRequest, stream coordinatorv1.CoordinatorService_GetWorkspaceBundleServer) error {
	if h.workspaceBundleStore == nil {
		return status.Error(codes.FailedPrecondition, "workspace bundle store is not configured")
	}
	if req == nil || !workspacebundle.ValidDigest(req.Digest) {
		return status.Error(codes.InvalidArgument, "valid workspace bundle digest is required")
	}
	file, size, err := h.workspaceBundleStore.Open(stream.Context(), req.Digest)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return status.Errorf(codes.NotFound, "workspace bundle not found: %v", err)
		}
		return status.Errorf(codes.Internal, "failed to open workspace bundle: %v", err)
	}
	defer func() { _ = file.Close() }()
	desc := &coordinatorv1.WorkspaceBundle{
		Digest: req.Digest,
		Size:   size,
	}
	for remaining, sequence := size, uint64(0); remaining > 0 || sequence == 0; sequence++ {
		chunkSize := min(remaining, int64(workspaceBundleChunkSize))
		chunk := &coordinatorv1.WorkspaceBundleChunk{
			Sequence: sequence,
			IsFinal:  chunkSize == remaining,
		}
		if sequence == 0 {
			chunk.Bundle = desc
		}
		if chunkSize > 0 {
			chunk.Data = make([]byte, chunkSize)
			if _, err := io.ReadFull(file, chunk.Data); err != nil {
				return status.Error(codes.Internal, "failed to read workspace bundle: "+err.Error())
			}
		}
		if err := stream.Send(chunk); err != nil {
			return status.Error(codes.Internal, "failed to send workspace bundle: "+err.Error())
		}
		remaining -= chunkSize
		if size == 0 {
			break
		}
	}
	return nil
}

func descriptorFromProto(desc *coordinatorv1.WorkspaceBundle) workspacebundle.Descriptor {
	if desc == nil {
		return workspacebundle.Descriptor{}
	}
	return workspacebundle.Descriptor{
		Digest:      desc.Digest,
		Size:        desc.Size,
		DAGPath:     desc.DagPath,
		OriginalRef: desc.OriginalRef,
		ResolvedRef: desc.ResolvedRef,
	}
}

func descriptorToProto(desc workspacebundle.Descriptor) *coordinatorv1.WorkspaceBundle {
	return &coordinatorv1.WorkspaceBundle{
		Digest:      desc.Digest,
		Size:        desc.Size,
		DagPath:     desc.DAGPath,
		OriginalRef: desc.OriginalRef,
		ResolvedRef: desc.ResolvedRef,
	}
}
