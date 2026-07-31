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
	name := ctx.Name
	if name == "" {
		return errors.New("context name is required")
	}
	if name == LocalContextName || name == currentFileName {
		return fmt.Errorf("%q and %q are reserved", LocalContextName, currentFileName)
	}
	if strings.ContainsAny(name, `/\`) {
		return errors.New("context name cannot contain path separators")
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.contextPath(name)); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	current, err := s.currentLocked()
	if err == nil && current == name {
		return s.writeCurrentLocked(LocalContextName)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) Get(_ context.Context, name string) (*Context, error) {
	if name == LocalContextName {
		return &Context{Name: LocalContextName}, nil
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
			readErrs = append(readErrs, fmt.Errorf("read %s: %w", entry.Name(), err))
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

func (s *Store) Use(ctx context.Context, name string) error {
	switch name {
	case "", LocalContextName:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.writeCurrentLocked(LocalContextName)
	default:
		if _, err := s.Get(ctx, name); err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.writeCurrentLocked(name)
	}
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
	data, err := os.ReadFile(path) //nolint:gosec // internal path
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var stored storedContext
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, err
	}
	return stored.toContext(s.encryptor)
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
