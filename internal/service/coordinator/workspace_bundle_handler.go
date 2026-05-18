// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const workspaceBundleChunkSize = 1 << 20

func (h *Handler) PutWorkspaceBundle(stream coordinatorv1.CoordinatorService_PutWorkspaceBundleServer) error {
	if h.workspaceBundleStore == nil {
		return status.Error(codes.FailedPrecondition, "workspace bundle store is not configured")
	}

	limits := workspacebundle.DefaultLimits()
	var desc workspacebundle.Descriptor
	var buf bytes.Buffer
	var sequence uint64

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if desc.Digest == "" {
				return stream.SendAndClose(&coordinatorv1.PutWorkspaceBundleResponse{
					Accepted: false,
					Error:    "workspace bundle descriptor is required",
				})
			}
			data := buf.Bytes()
			if err := h.workspaceBundleStore.Put(stream.Context(), desc, data); err != nil {
				return stream.SendAndClose(&coordinatorv1.PutWorkspaceBundleResponse{
					Accepted: false,
					Error:    err.Error(),
				})
			}
			return stream.SendAndClose(&coordinatorv1.PutWorkspaceBundleResponse{Accepted: true})
		}
		if err != nil {
			return status.Error(codes.Internal, "failed to receive workspace bundle: "+err.Error())
		}
		if chunk == nil {
			continue
		}
		if chunk.Sequence != sequence {
			return status.Error(codes.InvalidArgument, fmt.Sprintf("workspace bundle sequence mismatch: got %d, want %d", chunk.Sequence, sequence))
		}
		sequence++
		if chunk.Bundle != nil {
			next := descriptorFromProto(chunk.Bundle)
			if desc.Digest == "" {
				desc = next
			} else if desc.Digest != next.Digest {
				return status.Error(codes.InvalidArgument, "workspace bundle descriptor changed during upload")
			}
		}
		if len(chunk.Data) == 0 {
			continue
		}
		if int64(buf.Len()+len(chunk.Data)) > limits.MaxCompressedSize {
			return status.Error(codes.InvalidArgument, fmt.Sprintf("workspace bundle exceeds compressed size limit %d", limits.MaxCompressedSize))
		}
		if _, err := buf.Write(chunk.Data); err != nil {
			return status.Error(codes.Internal, "failed to buffer workspace bundle: "+err.Error())
		}
	}
}

func (h *Handler) HasWorkspaceBundle(_ context.Context, req *coordinatorv1.HasWorkspaceBundleRequest) (*coordinatorv1.HasWorkspaceBundleResponse, error) {
	if h.workspaceBundleStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "workspace bundle store is not configured")
	}
	if req == nil || !workspacebundle.ValidDigest(req.Digest) {
		return nil, status.Error(codes.InvalidArgument, "valid workspace bundle digest is required")
	}
	return &coordinatorv1.HasWorkspaceBundleResponse{
		Exists: h.workspaceBundleStore.Has(req.Digest),
	}, nil
}

func (h *Handler) GetWorkspaceBundle(req *coordinatorv1.GetWorkspaceBundleRequest, stream coordinatorv1.CoordinatorService_GetWorkspaceBundleServer) error {
	if h.workspaceBundleStore == nil {
		return status.Error(codes.FailedPrecondition, "workspace bundle store is not configured")
	}
	if req == nil || !workspacebundle.ValidDigest(req.Digest) {
		return status.Error(codes.InvalidArgument, "valid workspace bundle digest is required")
	}
	data, err := h.workspaceBundleStore.Get(stream.Context(), req.Digest)
	if err != nil {
		return status.Error(codes.NotFound, "workspace bundle not found: "+err.Error())
	}
	desc := &coordinatorv1.WorkspaceBundle{
		Digest: req.Digest,
		Size:   int64(len(data)),
	}
	for offset, sequence := 0, uint64(0); offset < len(data) || sequence == 0; sequence++ {
		end := min(offset+workspaceBundleChunkSize, len(data))
		chunk := &coordinatorv1.WorkspaceBundleChunk{
			Sequence: sequence,
			IsFinal:  end == len(data),
		}
		if sequence == 0 {
			chunk.Bundle = desc
		}
		if offset < len(data) {
			chunk.Data = data[offset:end]
		}
		if err := stream.Send(chunk); err != nil {
			return status.Error(codes.Internal, "failed to send workspace bundle: "+err.Error())
		}
		offset = end
		if len(data) == 0 {
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
