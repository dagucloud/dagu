// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

// Service joins the metadata store and the secret store. It makes each change of
// an endpoint and of its key.
type Service struct {
	store   Store
	secrets secret.Store

	// mu orders the changes of this process. The stores make each record change
	// atomic.
	mu sync.Mutex

	// models is nil until EnableModels. See models_service.go.
	models *modelsState
	// now gives the time of a models read. A test replaces it.
	now func() time.Time
}

// NewService creates a Service.
func NewService(store Store, secrets secret.Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("AI endpoint service: store cannot be nil")
	}
	if secrets == nil {
		return nil, errors.New("AI endpoint service: secret store cannot be nil")
	}
	return &Service{store: store, secrets: secrets, now: time.Now}, nil
}

// CreateInput holds the values of a new endpoint.
type CreateInput struct {
	Name        string
	Description string
	Preset      string
	Protocol    Protocol
	// BaseURL and Auth are optional. The preset gives the default.
	BaseURL   string
	Auth      Auth
	ModelsURL string
	Scope     Scope
	Allow     Allow
	// Key is the key of the provider. It is optional for the preset of a local
	// model only.
	Key   string
	Actor string
}

// UpdateInput holds the changes of an endpoint. A nil field keeps its value. The
// name, the preset, the protocol, the scope and the key have no field here.
type UpdateInput struct {
	Description *string
	BaseURL     *string
	Auth        *Auth
	ModelsURL   *string
	Allow       *Allow
	Actor       string
}

// Create validates and saves a new endpoint with its key.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Endpoint, error) {
	now := time.Now().UTC()
	ep := &Endpoint{
		Name:        in.Name,
		Description: in.Description,
		Preset:      in.Preset,
		Protocol:    in.Protocol,
		BaseURL:     in.BaseURL,
		Auth:        in.Auth,
		ModelsURL:   in.ModelsURL,
		Scope:       in.Scope,
		Allow:       Allow{Workspaces: slices.Clone(in.Allow.Workspaces), DAGs: slices.Clone(in.Allow.DAGs)},
		CreatedBy:   in.Actor,
		CreatedAt:   now,
		UpdatedBy:   in.Actor,
		UpdatedAt:   now,
	}
	row, err := applyPreset(ep)
	if err != nil {
		return nil, err
	}
	if err := validate(ep, row); err != nil {
		return nil, err
	}
	switch {
	case in.Key == "" && !row.AllowsNoKey():
		return nil, fmt.Errorf("%w: key is required for the preset %q", ErrInvalid, ep.Preset)
	case in.Key != "":
		if err := ValidateKey(in.Key); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.store.Get(ctx, ep.Scope, ep.Name); err == nil {
		return nil, ErrAlreadyExists
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	if s.models != nil {
		// A list that a failed delete left must not show for the new endpoint.
		if err := s.models.store.Delete(ctx, ep.Scope, ep.Name); err != nil {
			return nil, err
		}
	}

	if in.Key != "" {
		secretID, err := s.writeKey(ctx, ep, "", in.Key, in.Actor, now)
		if err != nil {
			return nil, err
		}
		ep.SecretID = secretID
		ep.KeyPrefix, ep.KeyFingerprint = KeyDisplay(in.Key)
		ep.KeyUpdatedAt = now
	}
	if err := s.store.Create(ctx, ep); err != nil {
		if ep.SecretID != "" {
			_ = s.secrets.Delete(context.WithoutCancel(ctx), ep.SecretID)
		}
		return nil, err
	}
	return ep.Clone(), nil
}

// Get returns the endpoint of a scope, or ErrNotFound.
func (s *Service) Get(ctx context.Context, scope Scope, name string) (*Endpoint, error) {
	return s.store.Get(ctx, scope, name)
}

// List returns the endpoints of all scopes. It gives no key.
func (s *Service) List(ctx context.Context) ([]*Endpoint, error) {
	return s.store.List(ctx)
}

// Update changes the fields of an endpoint that are not the key and not the scope.
func (s *Service) Update(ctx context.Context, scope Scope, name string, in UpdateInput) (*Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.store.Update(ctx, scope, name, func(ep *Endpoint) error {
		if in.Description != nil {
			ep.Description = *in.Description
		}
		if in.BaseURL != nil {
			ep.BaseURL = *in.BaseURL
		}
		if in.Auth != nil {
			ep.Auth = *in.Auth
		}
		if in.ModelsURL != nil {
			ep.ModelsURL = *in.ModelsURL
		}
		if in.Allow != nil {
			ep.Allow = Allow{Workspaces: slices.Clone(in.Allow.Workspaces), DAGs: slices.Clone(in.Allow.DAGs)}
		}
		row, ok := LookupPreset(ep.Preset, ep.Protocol)
		if !ok {
			return fmt.Errorf("%w: the stored preset %q has no row for the protocol %s", ErrInvalid, ep.Preset, ep.Protocol)
		}
		if err := validate(ep, row); err != nil {
			return err
		}
		ep.UpdatedBy = in.Actor
		ep.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// RotateKey sets the key of an endpoint. The old value stays in the secret store
// as an encrypted old version.
func (s *Service) RotateKey(ctx context.Context, scope Scope, name, key, actor string) (*Endpoint, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ep, err := s.store.Get(ctx, scope, name)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	secretID, err := s.writeKey(ctx, ep, ep.SecretID, key, actor, now)
	if err != nil {
		return nil, err
	}
	prefix, fingerprint := KeyDisplay(key)
	return s.store.Update(ctx, scope, name, func(ep *Endpoint) error {
		ep.SecretID = secretID
		ep.KeyPrefix, ep.KeyFingerprint = prefix, fingerprint
		ep.KeyUpdatedAt = now
		ep.UpdatedBy = actor
		ep.UpdatedAt = now
		return nil
	})
}

// Delete removes an endpoint, then its key, then its models list. A models list
// that stays is not an error: a later Create of the name deletes it.
func (s *Service) Delete(ctx context.Context, scope Scope, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ep, err := s.store.Get(ctx, scope, name)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, scope, name); err != nil {
		return err
	}
	if s.models != nil {
		if err := s.models.store.Delete(ctx, scope, name); err != nil {
			logger.Warn(ctx, "AI models list of a deleted endpoint was not deleted",
				slog.String("endpoint", name), slog.String("scope", scope.String()), slog.String("error", err.Error()))
		}
	}
	if ep.SecretID == "" {
		return nil
	}
	if err := s.secrets.Delete(ctx, ep.SecretID); err != nil && !errors.Is(err, secret.ErrNotFound) {
		return fmt.Errorf("AI endpoint %q was deleted but its key was not: %w", name, err)
	}
	return nil
}

// ResolveEndpoint finds the endpoint that a step names, for a DAG, and checks the
// use rule. A DAG with no workspace passes an empty workspace. It returns an
// *Error with the reason endpoint_unknown or endpoint_forbidden, or with the
// reason endpoint_unavailable when the store fails. The result has no key. The
// coordinator reads the key with ReadKey.
func (s *Service) ResolveEndpoint(ctx context.Context, name, dagName, workspace string) (Endpoint, error) {
	if err := ValidateName(name); err != nil {
		return Endpoint{}, &Error{Reason: ReasonUnknown, Name: name, Detail: "the name is not valid"}
	}
	ep, err := Resolve(name, dagName, workspace, func(scope Scope) (*Endpoint, error) {
		found, err := s.store.Get(ctx, scope, name)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, &Error{Reason: ReasonUnavailable, Name: name, Detail: "the endpoint store failed", Err: err}
		}
		return found, nil
	})
	if err != nil {
		return Endpoint{}, err
	}
	return *ep, nil
}

// ReadKey returns the key of an endpoint in clear text. It is for the relay of
// the coordinator only. No route of the API calls it, and no log or error holds
// the value. An endpoint with no key returns an empty key when its preset accepts
// no key. Otherwise it returns the reason endpoint_key_missing. A secret that the
// process cannot read gives the reason endpoint_unavailable.
func (s *Service) ReadKey(ctx context.Context, ep Endpoint) (string, error) {
	if !ep.HasKey() {
		if row, ok := LookupPreset(ep.Preset, ep.Protocol); ok && row.AllowsNoKey() {
			return "", nil
		}
		return "", &Error{Reason: ReasonKeyMissing, Name: ep.Name, Detail: "the endpoint has no key"}
	}
	key, _, err := s.secrets.ReadValue(ctx, ep.SecretID)
	switch {
	case err == nil:
		return key, nil
	case errors.Is(err, secret.ErrNotFound), errors.Is(err, secret.ErrNoValue):
		return "", &Error{Reason: ReasonKeyMissing, Name: ep.Name, Detail: "the endpoint has no key", Err: err}
	default:
		// The cause can name a path, never a key.
		return "", &Error{Reason: ReasonUnavailable, Name: ep.Name, Detail: "the process cannot read the key", Err: err}
	}
}

// Choices returns the endpoints that a DAG may use, sorted by name. A workspace
// endpoint hides the global endpoint of the same name for the DAGs of its
// workspace, even when the DAG may not use the workspace endpoint.
func (s *Service) Choices(ctx context.Context, dagName, workspace string) ([]Endpoint, error) {
	all, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	hidden := map[string]bool{}
	for _, ep := range all {
		if !ep.Scope.IsGlobal() && ep.Scope.Workspace == workspace {
			hidden[ep.Name] = true
		}
	}
	var out []Endpoint
	for _, ep := range all {
		if ep.Scope.IsGlobal() && hidden[ep.Name] {
			continue
		}
		if !ep.Scope.IsGlobal() && ep.Scope.Workspace != workspace {
			continue
		}
		if ep.CheckUse(dagName, workspace) == nil {
			out = append(out, *ep)
		}
	}
	slices.SortFunc(out, func(a, b Endpoint) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// writeKey stores a new version of the key and returns the ID of the secret. An
// empty secretID means that the endpoint has no secret yet. A secret of the same
// ref, with no endpoint, is the rest of a failed delete: the call reuses it.
func (s *Service) writeKey(ctx context.Context, ep *Endpoint, secretID, key, actor string, now time.Time) (string, error) {
	return writeManagedSecret(ctx, s.secrets, managedSecret{
		workspace:   ep.Scope.SecretWorkspace(),
		ref:         SecretRef(ep.Name),
		description: fmt.Sprintf("Key of the AI endpoint %s", ep.Name),
	}, secretID, key, actor, now)
}

// managedSecret names a managed secret of the secret store.
type managedSecret struct {
	workspace   string
	ref         string
	description string
}

// writeManagedSecret stores a new version of a value and returns the ID of the
// secret. An empty secretID means that no secret exists yet. A secret of the same
// ref that the record does not know is the rest of a failed delete: the call
// reuses it. A secret of the record that no longer exists, because an
// administrator deleted it through the secret routes, is made again.
func writeManagedSecret(ctx context.Context, secrets secret.Store, target managedSecret, secretID, value, actor string, now time.Time) (string, error) {
	input := secret.WriteValueInput{Value: value, CreatedBy: actor, CreatedAt: now}
	if secretID != "" {
		if _, err := secrets.WriteValue(ctx, secretID, input); !errors.Is(err, secret.ErrNotFound) {
			if err != nil {
				return "", err
			}
			return secretID, nil
		}
		secretID = ""
	}
	if secretID == "" {
		sec, err := secret.New(secret.CreateInput{
			Workspace:    target.workspace,
			Ref:          target.ref,
			Description:  target.description,
			ProviderType: secret.ProviderDaguManaged,
			CreatedBy:    actor,
		}, now)
		if err != nil {
			return "", err
		}
		err = secrets.Create(ctx, sec, &input)
		if err == nil {
			return sec.ID, nil
		}
		if !errors.Is(err, secret.ErrAlreadyExists) {
			return "", err
		}
		old, getErr := secrets.GetByRef(ctx, target.workspace, target.ref)
		if getErr != nil {
			return "", getErr
		}
		secretID = old.ID
	}
	if _, err := secrets.WriteValue(ctx, secretID, input); err != nil {
		return "", err
	}
	return secretID, nil
}

// applyPreset fills the defaults of the preset in ep and returns the row.
func applyPreset(ep *Endpoint) (Preset, error) {
	if !presetKnown(ep.Preset) {
		return Preset{}, fmt.Errorf("%w: preset %q: expected %s or a preset of the table", ErrInvalid, ep.Preset, PresetCustom)
	}
	row, ok := LookupPreset(ep.Preset, ep.Protocol)
	if !ok {
		return Preset{}, fmt.Errorf("%w: preset %q has no row for the protocol %q", ErrInvalid, ep.Preset, ep.Protocol)
	}
	if ep.BaseURL == "" {
		ep.BaseURL = row.BaseURL
	}
	if ep.Auth == "" {
		ep.Auth = row.Auth
	}
	return row, nil
}

// validate checks the fields of an endpoint, after the defaults of the preset.
func validate(ep *Endpoint, row Preset) error {
	if err := ValidateName(ep.Name); err != nil {
		return err
	}
	if err := validateDescription(ep.Description); err != nil {
		return err
	}
	if ep.BaseURL == "" {
		return fmt.Errorf("%w: base_url is required for the preset %q", ErrInvalid, ep.Preset)
	}
	if err := validateURL("base_url", ep.BaseURL, row.allowsHTTP()); err != nil {
		return err
	}
	switch ep.Auth {
	case AuthXAPIKey, AuthBearer:
	case AuthAnthropicOAuth:
		if ep.Protocol != ProtocolAnthropic {
			return fmt.Errorf("%w: auth %q is valid with the protocol anthropic only", ErrInvalid, ep.Auth)
		}
	case "":
		return fmt.Errorf("%w: auth is required for the preset %q", ErrInvalid, ep.Preset)
	default:
		return fmt.Errorf("%w: auth %q: expected x-api-key, bearer or anthropic-oauth", ErrInvalid, ep.Auth)
	}
	if ep.ModelsURL != "" {
		if ep.Preset != PresetCustom {
			return fmt.Errorf("%w: models_url is valid with the preset %s only", ErrInvalid, PresetCustom)
		}
		if err := validateURL("models_url", ep.ModelsURL, true); err != nil {
			return err
		}
	}
	return validateAllow(ep.Scope, ep.Allow)
}
