// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clicontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	LocalContextName = "local"

	fileExtension   = ".json"
	currentFileName = "current"
	dirPermissions  = 0750
	filePermissions = 0600
)

var (
	ErrNotFound      = errors.New("context not found")
	ErrAlreadyExists = errors.New("context already exists")
)

type Context struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	ServerURL      string `json:"server_url"`
	APIKey         string `json:"api_key,omitempty"`
	SkipTLSVerify  bool   `json:"skip_tls_verify,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type storedContext struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	ServerURL      string `json:"server_url"`
	APIKeyEnc      string `json:"api_key_enc,omitempty"`
	SkipTLSVerify  bool   `json:"skip_tls_verify,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (s storedContext) toContext(enc *crypto.Encryptor) (*Context, error) {
	apiKey, err := enc.Decrypt(s.APIKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt api key: %w", err)
	}
	return &Context{
		Name:           s.Name,
		Description:    s.Description,
		ServerURL:      s.ServerURL,
		APIKey:         apiKey,
		SkipTLSVerify:  s.SkipTLSVerify,
		TimeoutSeconds: s.TimeoutSeconds,
	}, nil
}

func newStoredContext(ctx *Context, enc *crypto.Encryptor) (*storedContext, error) {
	apiKeyEnc, err := enc.Encrypt(ctx.APIKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt api key: %w", err)
	}
	return &storedContext{
		Name:           ctx.Name,
		Description:    ctx.Description,
		ServerURL:      ctx.ServerURL,
		APIKeyEnc:      apiKeyEnc,
		SkipTLSVerify:  ctx.SkipTLSVerify,
		TimeoutSeconds: ctx.TimeoutSeconds,
	}, nil
}

type Store struct {
	baseDir   string
	encryptor *crypto.Encryptor
	mu        sync.RWMutex
}

func NewStore(baseDir string, enc *crypto.Encryptor) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("clicontext: baseDir cannot be empty")
	}
	if enc == nil {
		return nil, errors.New("clicontext: encryptor cannot be nil")
	}
	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("clicontext: create directory: %w", err)
	}
	return &Store{baseDir: baseDir, encryptor: enc}, nil
}

func (s *Store) ValidateContext(ctx *Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	normalizeContext(ctx)
	if err := validateStoredName(ctx.Name); err != nil {
		return err
	}
	if !strings.HasPrefix(ctx.APIKey, "dagu_") {
		return errors.New("api key must use the dagu_ prefix")
	}
	u, err := url.Parse(ctx.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("server URL must use http or https")
	}
	if u.Host == "" {
		return errors.New("server URL must include a host")
	}
	if ctx.TimeoutSeconds < 0 {
		return errors.New("timeout must not be negative")
	}
	return nil
}

func (s *Store) Create(_ context.Context, ctx *Context) error {
	if err := s.ValidateContext(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.contextPath(ctx.Name)
	if _, err := os.Stat(path); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.writeContext(path, ctx)
}

func (s *Store) Update(_ context.Context, ctx *Context) error {
	if err := s.ValidateContext(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.contextPath(ctx.Name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return s.writeContext(path, ctx)
}

func (s *Store) Delete(_ context.Context, name string) error {
	if name == "" || name == LocalContextName {
		return ErrNotFound
	}
	if err := validateStoredName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.contextPath(name)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	// A directory or other non-regular entry at this path is not a stored
	// context, so it must not be removed or drive a marker change.
	if !info.Mode().IsRegular() {
		return ErrNotFound
	}
	// Move the current marker off the context before the file disappears, so a
	// failure to update the marker cannot leave it naming a deleted context.
	current, err := s.currentLocked()
	switch {
	case err == nil && current == name:
		if err := s.writeCurrentLocked(LocalContextName); err != nil {
			return err
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}

func (s *Store) Get(_ context.Context, name string) (*Context, error) {
	if name == LocalContextName {
		return &Context{Name: LocalContextName}, nil
	}
	if err := validateStoredName(name); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readContext(s.contextPath(name))
}

func (s *Store) List(_ context.Context) ([]*Context, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, err
	}
	var contexts []*Context
	var readErrs []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != fileExtension {
			continue
		}
		if entry.Name() == currentFileName+fileExtension {
			continue
		}
		ctx, err := s.readContext(filepath.Join(s.baseDir, entry.Name()))
		if err != nil {
			readErrs = append(readErrs, err)
			continue
		}
		contexts = append(contexts, ctx)
	}
	sort.Slice(contexts, func(i, j int) bool { return contexts[i].Name < contexts[j].Name })
	if len(readErrs) > 0 {
		return contexts, errors.Join(readErrs...)
	}
	return contexts, nil
}

func (s *Store) Current(_ context.Context) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, err := s.currentLocked()
	if errors.Is(err, os.ErrNotExist) {
		return LocalContextName, nil
	}
	return current, err
}

func (s *Store) Use(_ context.Context, name string) error {
	if name == "" || name == LocalContextName {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.writeCurrentLocked(LocalContextName)
	}
	if err := validateStoredName(name); err != nil {
		return err
	}
	// Read the context and write the marker under one exclusive lock so the
	// marker cannot end up naming a context removed after the check.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readContext(s.contextPath(name)); err != nil {
		return err
	}
	return s.writeCurrentLocked(name)
}

// validateStoredName reports whether name may address a context file in the
// store directory. Reserved names and anything that does not resolve to a flat
// file name directly inside the directory are rejected. Every caller must pass
// a name through this before building a path with contextPath.
func validateStoredName(name string) error {
	if name == "" {
		return errors.New("context name is required")
	}
	if name == LocalContextName || name == currentFileName {
		return fmt.Errorf("%q and %q are reserved", LocalContextName, currentFileName)
	}
	if strings.ContainsAny(name, `/\`) {
		return errors.New("context name cannot contain path separators")
	}
	if !filepath.IsLocal(name) {
		return fmt.Errorf("invalid context name %q", name)
	}
	return nil
}

func (s *Store) contextPath(name string) string {
	return filepath.Join(s.baseDir, name+fileExtension)
}

func (s *Store) writeContext(path string, ctx *Context) error {
	stored, err := newStoredContext(ctx, s.encryptor)
	if err != nil {
		return err
	}
	return fileutil.WriteJSONAtomic(path, stored, filePermissions)
}

func (s *Store) readContext(path string) (*Context, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from a validated context name
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	file := filepath.Base(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	var stored storedContext
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode %s: %w", file, err)
	}
	ctx, err := stored.toContext(s.encryptor)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return ctx, nil
}

func normalizeContext(ctx *Context) {
	if ctx == nil {
		return
	}
	ctx.Name = strings.TrimSpace(ctx.Name)
	ctx.ServerURL = strings.TrimSpace(ctx.ServerURL)
	ctx.APIKey = strings.TrimSpace(ctx.APIKey)
}

func (s *Store) currentLocked() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.baseDir, currentFileName))
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return LocalContextName, nil
	}
	return name, nil
}

func (s *Store) writeCurrentLocked(name string) error {
	return fileutil.WriteFileAtomic(filepath.Join(s.baseDir, currentFileName), []byte(name), filePermissions)
}
