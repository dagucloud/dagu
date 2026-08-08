// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
)

type contextKey struct{}

type contextValue struct {
	service *Service
	source  Source
}

func WithContext(ctx context.Context, service *Service, source Source) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if service == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, contextValue{
		service: service,
		source:  normalizeSource(source),
	})
}

func FromContext(ctx context.Context) (*Service, Source, bool) {
	if ctx == nil {
		return nil, Source{}, false
	}
	v, ok := ctx.Value(contextKey{}).(contextValue)
	if !ok || v.service == nil {
		return nil, Source{}, false
	}
	return v.service, v.source, true
}

func SourceFromContext(ctx context.Context) (Source, bool) {
	_, source, ok := FromContext(ctx)
	return source, ok
}

func EmitPersistedStatusFromContext(ctx context.Context, status *dagrun.DAGRunStatus) error {
	_, _, err := EmitPersistedStatusTransitionFromContext(ctx, "", status, nil)
	return err
}

func EmitPersistedStatusTransitionFromContext(
	ctx context.Context,
	previous EventType,
	status *dagrun.DAGRunStatus,
	data map[string]any,
) (EventType, bool, error) {
	service, source, ok := FromContext(ctx)
	if !ok || service == nil || status == nil {
		return previous, false, nil
	}
	lifecycleEventType, ok := PersistedDAGRunEventTypeForStatus(status.Status)
	if !ok {
		return previous, false, nil
	}
	eventType := lifecycleEventType
	if lifecycleEventType == previous {
		eventType = TypeDAGRunUpdated
	}
	if err := service.Emit(context.WithoutCancel(ctx), NewDAGRunEvent(source, eventType, status, data)); err != nil {
		return previous, false, err
	}
	return lifecycleEventType, true, nil
}
