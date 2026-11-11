// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
)

// modelsState holds what the service needs for the models lists. It exists only
// after EnableModels.
type modelsState struct {
	store  ModelsStore
	reader *ModelsReader
	// locks holds one mutex for each endpoint, so two reads of one endpoint never
	// run together.
	locks sync.Map
	// background counts the reads that a create or a rotation starts.
	background sync.WaitGroup
	// onDemand holds the time of the last on-demand refresh of each endpoint, which
	// the model check starts. See CheckModel.
	refreshMu sync.Mutex
	onDemand  map[string]time.Time
}

// EnableModels turns on the models list: the service then reads, keeps and
// serves the lists. A service that never calls it has no lists, and the models
// functions give the reason ErrModelsDisabled.
func (s *Service) EnableModels(store ModelsStore, reader *ModelsReader) error {
	if store == nil || reader == nil {
		return errors.New("AI endpoint service: the models store and the models reader cannot be nil")
	}
	s.models = &modelsState{store: store, reader: reader}
	return nil
}

// ErrModelsDisabled means that the service was not given a models store.
var ErrModelsDisabled = errors.New("the AI endpoint service has no models list")

// ModelsEnabled tells if the service keeps models lists.
func (s *Service) ModelsEnabled() bool { return s.models != nil }

// WaitModels waits for the reads that RefreshModelsAsync started. A test uses it.
func (s *Service) WaitModels() {
	if s.models != nil {
		s.models.background.Wait()
	}
}

// ModelsOf returns the list of an endpoint, or an empty list that has no date
// when the endpoint was never read. The list of an endpoint that has no models
// URL is always empty. An error of the store is returned.
func (s *Service) ModelsOf(ctx context.Context, ep *Endpoint) (*Models, error) {
	empty := &Models{Scope: ep.Scope, Name: ep.Name}
	if s.models == nil || ep.ListURL() == "" {
		return empty, nil
	}
	found, err := s.models.store.Get(ctx, ep.Scope, ep.Name)
	switch {
	case errors.Is(err, ErrNotFound):
		return empty, nil
	case err != nil:
		return nil, err
	}
	return found, nil
}

// ModelsByEndpoint returns the lists of all endpoints, by scope and name. An
// endpoint with no list has no item.
func (s *Service) ModelsByEndpoint(ctx context.Context) (map[ModelsKey]*Models, error) {
	out := map[ModelsKey]*Models{}
	if s.models == nil {
		return out, nil
	}
	all, err := s.models.store.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range all {
		out[ModelsKey{Scope: m.Scope, Name: m.Name}] = m
	}
	return out, nil
}

// ModelsKey identifies the list of an endpoint.
type ModelsKey struct {
	Scope Scope
	Name  string
}

// RefreshModels reads the models list of an endpoint from the provider now, and
// keeps the result. A failed read keeps the last good list, and records the error
// with its time. The error of the provider is in the result, not in the returned
// error. The returned error means that the endpoint does not exist
// (ErrNotFound), that it has no models URL (ErrNoModelsURL), or that a store
// failed.
func (s *Service) RefreshModels(ctx context.Context, scope Scope, name string) (*Models, error) {
	if s.models == nil {
		return nil, ErrModelsDisabled
	}
	unlock := s.lockModels(scope, name)
	defer unlock()
	return s.refreshLocked(ctx, scope, name)
}

// RefreshModelsIfDue reads the list when its last read is older than period. It
// returns false when the list is not due, or when the endpoint has no models URL
// or does not exist any more.
func (s *Service) RefreshModelsIfDue(ctx context.Context, scope Scope, name string, period time.Duration, now time.Time) (bool, error) {
	if s.models == nil {
		return false, ErrModelsDisabled
	}
	unlock := s.lockModels(scope, name)
	defer unlock()
	ep, err := s.store.Get(ctx, scope, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if ep.ListURL() == "" {
		return false, nil
	}
	current, err := s.ModelsOf(ctx, ep)
	if err != nil {
		return false, err
	}
	if !current.AttemptedAt.IsZero() && now.Before(current.AttemptedAt.Add(period)) {
		return false, nil
	}
	if _, err := s.refreshLocked(ctx, scope, name); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNoModelsURL) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RefreshModelsAsync starts the first read of a new endpoint or of a new key, and
// returns at once: the save of a key never waits for the provider. The read is
// bounded by the models timeout and does not end with ctx.
func (s *Service) RefreshModelsAsync(ctx context.Context, scope Scope, name string) {
	if s.models == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	s.models.background.Go(func() {
		if _, err := s.RefreshModels(ctx, scope, name); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNoModelsURL) {
			logger.Warn(ctx, "AI models refresh failed", slog.String("endpoint", name), slog.String("scope", scope.String()), slog.String("error", err.Error()))
		}
	})
}

func (s *Service) lockModels(scope Scope, name string) func() {
	value, _ := s.models.locks.LoadOrStore(scope.String()+"/"+name, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// refreshLocked reads and keeps the list. The caller holds the lock of the
// endpoint.
func (s *Service) refreshLocked(ctx context.Context, scope Scope, name string) (*Models, error) {
	ep, err := s.store.Get(ctx, scope, name)
	if err != nil {
		return nil, err
	}
	if ep.ListURL() == "" {
		return nil, ErrNoModelsURL
	}
	previous, err := s.ModelsOf(ctx, ep)
	if err != nil {
		return nil, err
	}
	next := previous.Clone()

	list, readErr := s.readModels(ctx, ep)
	now := s.now().UTC()
	next.AttemptedAt = now
	if readErr != nil {
		next.LastError = readErr.Error()
		next.LastErrorAt = now
		logger.Warn(ctx, "AI models read failed",
			slog.String("endpoint", ep.Name), slog.String("scope", ep.Scope.String()), slog.String("error", next.LastError))
	} else {
		next.Names, next.Info = list.Names, list.Info
		next.RefreshedAt = now
		next.LastError = ""
		next.LastErrorAt = time.Time{}
		logger.Info(ctx, "AI models read",
			slog.String("endpoint", ep.Name), slog.String("scope", ep.Scope.String()), slog.Int("models", len(list.Names)))
	}

	// The endpoint can be deleted, or deleted and created again, or get another
	// URL or key while the read runs. The change lock orders the check and the
	// save against Delete. A list of an endpoint that changed is not saved.
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.store.Get(ctx, scope, name)
	if err != nil {
		return nil, err
	}
	if !current.CreatedAt.Equal(ep.CreatedAt) || current.ListURL() != ep.ListURL() || current.SecretID != ep.SecretID ||
		!current.KeyUpdatedAt.Equal(ep.KeyUpdatedAt) || current.Auth != ep.Auth {
		return nil, fmt.Errorf("AI endpoint %q changed while its models list was read; the list is not saved", name)
	}
	if err := s.models.store.Put(ctx, next); err != nil {
		return nil, err
	}
	return next.Clone(), nil
}

// readModels reads the key and calls the provider. The error text never has the key.
func (s *Service) readModels(ctx context.Context, ep *Endpoint) (ModelsList, error) {
	key, err := s.ReadKey(ctx, *ep)
	if err != nil {
		return ModelsList{}, err
	}
	return s.models.reader.ReadList(ctx, *ep, key)
}
