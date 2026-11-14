// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
)

var _ publicwebhook.Store = (*PublicWebhookStore)(nil)

const (
	publicWebhookStatusID  = "status"
	publicWebhookLogPrefix = "log/"
)

// PublicWebhookStore implements [publicwebhook.Store] over a collection.
type PublicWebhookStore struct {
	col persis.Collection
}

// NewPublicWebhookStore creates a PublicWebhookStore backed by col.
func NewPublicWebhookStore(col persis.Collection) *PublicWebhookStore {
	return &PublicWebhookStore{col: col}
}

// Log returns a webhook's log; an empty one when it has none.
func (s *PublicWebhookStore) Log(ctx context.Context, webhookID string) (publicwebhook.Log, error) {
	var log publicwebhook.Log
	_, err := getRecord(ctx, s.col, publicWebhookLogPrefix+webhookID, &log)
	return log, err
}

// AddDelivery puts d first in a webhook's log, replacing an entry for the
// same delivery, and keeps the latest LogSize.
func (s *PublicWebhookStore) AddDelivery(ctx context.Context, webhookID string, d publicwebhook.Delivery) error {
	return updateRecord(ctx, s.col, publicWebhookLogPrefix+webhookID, func(log *publicwebhook.Log) {
		earlier := slices.DeleteFunc(log.Deliveries, func(e publicwebhook.Delivery) bool { return e.ID == d.ID })
		log.Deliveries = append([]publicwebhook.Delivery{d}, earlier[:min(len(earlier), publicwebhook.LogSize-1)]...)
	})
}

// AddMissed adds to a webhook's missed counts.
func (s *PublicWebhookStore) AddMissed(ctx context.Context, webhookID string, expired, refused int, at time.Time) error {
	return updateRecord(ctx, s.col, publicWebhookLogPrefix+webhookID, func(log *publicwebhook.Log) {
		if log.Missed.Expired == 0 && log.Missed.Refused == 0 {
			log.Missed.Since = at
		}
		log.Missed.Expired += expired
		log.Missed.Refused += refused
	})
}

// ClearMissed dismisses a webhook's missed counts.
func (s *PublicWebhookStore) ClearMissed(ctx context.Context, webhookID string) error {
	return updateRecord(ctx, s.col, publicWebhookLogPrefix+webhookID, func(log *publicwebhook.Log) {
		log.Missed = publicwebhook.Missed{}
	})
}

// DeleteLog removes a webhook's log.
func (s *PublicWebhookStore) DeleteLog(ctx context.Context, webhookID string) error {
	return s.col.Delete(ctx, publicWebhookLogPrefix+webhookID)
}

// Status returns the relay's status; the zero Status before any.
func (s *PublicWebhookStore) Status(ctx context.Context) (publicwebhook.Status, error) {
	var status publicwebhook.Status
	_, err := getRecord(ctx, s.col, publicWebhookStatusID, &status)
	return status, err
}

// SetStatus replaces the relay's status.
func (s *PublicWebhookStore) SetStatus(ctx context.Context, status publicwebhook.Status) error {
	data, err := persis.Encode(status)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.col.Put(ctx, &persis.Record{ID: publicWebhookStatusID, Data: data, CreatedAt: now, UpdatedAt: now})
}

// getRecord decodes record id into v, leaving v as it is when there is no
// such record, and reports whether there was.
func getRecord[T any](ctx context.Context, col persis.Collection, id string, v *T) (*persis.Record, error) {
	rec, err := col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := persis.Decode(rec, v); err != nil {
		return nil, fmt.Errorf("decode record %q: %w", id, err)
	}
	return rec, nil
}

// updateRecord applies mutate to record id, or to the zero value when there
// is none, and stores the result. A concurrent update from another process
// is never overwritten: mutate runs again on the newer record.
func updateRecord[T any](ctx context.Context, col persis.Collection, id string, mutate func(*T)) error {
	return retryConflict(ctx, func(ctx context.Context) error {
		var v T
		rec, err := getRecord(ctx, col, id, &v)
		if err != nil {
			return err
		}
		mutate(&v)
		data, err := persis.Encode(v)
		if err != nil {
			return err
		}
		if rec == nil {
			now := time.Now().UTC()
			return col.Create(ctx, &persis.Record{ID: id, Data: data, CreatedAt: now, UpdatedAt: now})
		}
		err = col.CompareAndSwap(ctx, id, rec.Data, data)
		if errors.Is(err, persis.ErrNotFound) {
			// Deleted since it was read: start over from no record.
			return persis.ErrConflict
		}
		return err
	})
}
