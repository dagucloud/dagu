// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/dagstate"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetState returns the current state entry for a reference, if it exists.
func (h *Handler) GetState(ctx context.Context, req *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error) {
	if h.stateStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "state store is not configured")
	}
	ref, err := stateRefFromProto(req.GetRef())
	if err != nil {
		return nil, stateRPCError(err)
	}
	entry, err := h.stateStore.Get(ctx, ref)
	if err != nil {
		if errors.Is(err, dagstate.ErrNotFound) {
			return &coordinatorv1.GetStateResponse{Found: false}, nil
		}
		return nil, stateRPCError(err)
	}
	return &coordinatorv1.GetStateResponse{Found: true, Entry: stateEntryToProto(entry)}, nil
}

// PutState creates or updates a state entry through the coordinator state store.
func (h *Handler) PutState(ctx context.Context, req *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
	if h.stateStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "state store is not configured")
	}
	ref, err := stateRefFromProto(req.GetRef())
	if err != nil {
		return nil, stateRPCError(err)
	}
	var expected *int64
	if req.GetHasExpectedVersion() {
		v := req.GetExpectedVersion()
		expected = &v
	}
	entry, err := h.stateStore.Put(ctx, ref, json.RawMessage(req.GetValue()), dagstate.PutOptions{
		ExpectedVersion: expected,
		CreateOnly:      req.GetCreateOnly(),
		UpdatedBy:       stateUpdateSourceFromProto(req.GetUpdatedBy()),
	})
	if err != nil {
		return nil, stateRPCError(err)
	}
	return &coordinatorv1.PutStateResponse{Entry: stateEntryToProto(entry)}, nil
}

// DeleteState removes a state entry through the coordinator state store.
func (h *Handler) DeleteState(ctx context.Context, req *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
	if h.stateStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "state store is not configured")
	}
	ref, err := stateRefFromProto(req.GetRef())
	if err != nil {
		return nil, stateRPCError(err)
	}
	deleted, err := h.stateStore.Delete(ctx, ref)
	if err != nil {
		return nil, stateRPCError(err)
	}
	return &coordinatorv1.DeleteStateResponse{Deleted: deleted}, nil
}

// ListState returns state entries matching the requested scope and key prefix.
func (h *Handler) ListState(ctx context.Context, req *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
	if h.stateStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "state store is not configured")
	}
	entries, err := h.stateStore.List(ctx, dagstate.ListOptions{
		Scope:     dagstate.Scope(req.GetScope()),
		Namespace: req.GetNamespace(),
		KeyPrefix: req.GetKeyPrefix(),
		Limit:     int(req.GetLimit()),
	})
	if err != nil {
		return nil, stateRPCError(err)
	}
	out := make([]*coordinatorv1.StateEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, stateEntryToProto(entry))
	}
	return &coordinatorv1.ListStateResponse{Entries: out}, nil
}

func stateRefFromProto(ref *coordinatorv1.StateRef) (dagstate.Ref, error) {
	if ref == nil {
		return dagstate.Ref{}, fmt.Errorf("%w: ref is required", dagstate.ErrInvalidRef)
	}
	out := dagstate.Ref{
		Scope:     dagstate.Scope(ref.GetScope()),
		Namespace: ref.GetNamespace(),
		Key:       ref.GetKey(),
	}
	return out, out.Validate()
}

func stateEntryToProto(entry *dagstate.Entry) *coordinatorv1.StateEntry {
	if entry == nil {
		return nil
	}
	return &coordinatorv1.StateEntry{
		Ref:       stateRefToProto(entry.Ref),
		Value:     append([]byte(nil), entry.Value...),
		Version:   entry.Version,
		Hash:      entry.Hash,
		CreatedAt: entry.CreatedAt.UnixNano(),
		UpdatedAt: entry.UpdatedAt.UnixNano(),
		UpdatedBy: stateUpdateSourceToProto(entry.UpdatedBy),
	}
}

func stateRefToProto(ref dagstate.Ref) *coordinatorv1.StateRef {
	return &coordinatorv1.StateRef{
		Scope:     string(ref.Scope),
		Namespace: ref.Namespace,
		Key:       ref.Key,
	}
}

func stateUpdateSourceFromProto(src *coordinatorv1.StateUpdateSource) *dagstate.UpdateSource {
	if src == nil {
		return nil
	}
	return &dagstate.UpdateSource{
		DAGName:   src.GetDagName(),
		DAGRunID:  src.GetDagRunId(),
		AttemptID: src.GetAttemptId(),
		StepName:  src.GetStepName(),
	}
}

func stateUpdateSourceToProto(src *dagstate.UpdateSource) *coordinatorv1.StateUpdateSource {
	if src == nil {
		return nil
	}
	return &coordinatorv1.StateUpdateSource{
		DagName:   src.DAGName,
		DagRunId:  src.DAGRunID,
		AttemptId: src.AttemptID,
		StepName:  src.StepName,
	}
}

func stateRPCError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dagstate.ErrInvalidRef), errors.Is(err, dagstate.ErrInvalidValue), errors.Is(err, dagstate.ErrValueTooLarge):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, dagstate.ErrConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, dagstate.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
