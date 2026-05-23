// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package session implements [agent.SessionStore] using a [persis.Collection].
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ agent.SessionStore = (*Store)(nil)

const maxTitleLength = 50

// Store implements [agent.SessionStore].
// Three in-memory indices (byUser, byParent, updatedAt) are rebuilt from
// the collection on startup and kept in sync under mu.
type Store struct {
	col        persis.Collection
	maxPerUser int

	mu        sync.RWMutex
	byUser    map[string][]string  // userID → []sessionID (sorted newest-first)
	byParent  map[string][]string  // parentSessionID → []childSessionID
	updatedAt map[string]time.Time // sessionID → UpdatedAt
}

// storedSession is the on-wire format stored in the collection.
type storedSession struct {
	ID              string          `json:"id"`
	UserID          string          `json:"user_id"`
	DAGName         string          `json:"dag_name,omitempty"`
	Title           string          `json:"title,omitempty"`
	Model           string          `json:"model,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ParentSessionID string          `json:"parent_session_id,omitempty"`
	DelegateTask    string          `json:"delegate_task,omitempty"`
	Messages        []agent.Message `json:"messages"`
}

func (s *storedSession) toSession() *agent.Session {
	return &agent.Session{
		ID:              s.ID,
		UserID:          s.UserID,
		DAGName:         s.DAGName,
		Title:           s.Title,
		Model:           s.Model,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		ParentSessionID: s.ParentSessionID,
		DelegateTask:    s.DelegateTask,
	}
}

// Option configures a Store.
type Option func(*Store)

// WithMaxPerUser sets the per-user session cap.
// When a user exceeds the cap the oldest top-level sessions are purged.
func WithMaxPerUser(n int) Option {
	return func(s *Store) { s.maxPerUser = n }
}

// New creates a Store backed by col.
func New(col persis.Collection, opts ...Option) (*Store, error) {
	s := &Store{
		col:       col,
		byUser:    make(map[string][]string),
		byParent:  make(map[string][]string),
		updatedAt: make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(s)
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("session store: build index: %w", err)
	}
	return s, nil
}

func (s *Store) rebuildIndex(ctx context.Context) error {
	page, err := s.col.List(ctx, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range page.Records {
		var ss storedSession
		if err := persis.Decode(rec, &ss); err != nil {
			continue
		}
		s.byUser[ss.UserID] = append(s.byUser[ss.UserID], ss.ID)
		s.updatedAt[ss.ID] = ss.UpdatedAt
		if ss.ParentSessionID != "" {
			s.byParent[ss.ParentSessionID] = append(s.byParent[ss.ParentSessionID], ss.ID)
		}
	}
	for userID := range s.byUser {
		s.sortUserSessions(userID)
	}
	return nil
}

// CreateSession creates a new session.
func (s *Store) CreateSession(ctx context.Context, sess *agent.Session) error {
	if err := validateSession(sess, true); err != nil {
		return err
	}

	ss := fromSession(sess, nil)
	data, enc, err := persis.Encode(ss)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.col.Put(ctx, &persis.Record{
		ID:        sess.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
	}); err != nil {
		return err
	}

	s.byUser[sess.UserID] = append(s.byUser[sess.UserID], sess.ID)
	s.updatedAt[sess.ID] = sess.UpdatedAt
	if sess.ParentSessionID != "" {
		s.byParent[sess.ParentSessionID] = append(s.byParent[sess.ParentSessionID], sess.ID)
	}
	s.sortUserSessions(sess.UserID)
	s.enforceMaxLocked(ctx, sess.UserID)
	return nil
}

// GetSession retrieves a session by ID.
func (s *Store) GetSession(ctx context.Context, id string) (*agent.Session, error) {
	if id == "" {
		return nil, agent.ErrInvalidSessionID
	}
	ss, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	return ss.toSession(), nil
}

// ListSessions returns all sessions for a user, sorted by UpdatedAt descending.
func (s *Store) ListSessions(ctx context.Context, userID string) ([]*agent.Session, error) {
	if userID == "" {
		return nil, agent.ErrInvalidUserID
	}
	s.mu.RLock()
	ids := make([]string, len(s.byUser[userID]))
	copy(ids, s.byUser[userID])
	s.mu.RUnlock()

	out := make([]*agent.Session, 0, len(ids))
	for _, id := range ids {
		sess, err := s.GetSession(ctx, id)
		if err != nil {
			if errors.Is(err, agent.ErrSessionNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

// UpdateSession updates session metadata (Title, UpdatedAt).
func (s *Store) UpdateSession(ctx context.Context, sess *agent.Session) error {
	if err := validateSession(sess, false); err != nil {
		return err
	}

	rec, err := s.col.Get(ctx, sess.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return agent.ErrSessionNotFound
		}
		return err
	}
	var existing storedSession
	if err := persis.Decode(rec, &existing); err != nil {
		return fmt.Errorf("session store: decode for Update: %w", err)
	}

	existing.Title = sess.Title
	existing.UpdatedAt = sess.UpdatedAt

	data, enc, err := persis.Encode(&existing)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        rec.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
	}); err != nil {
		return err
	}

	s.mu.Lock()
	s.updatedAt[sess.ID] = sess.UpdatedAt
	s.sortUserSessions(existing.UserID)
	s.mu.Unlock()
	return nil
}

// DeleteSession removes a session and all its messages.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if id == "" {
		return agent.ErrInvalidSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLockedCtx(ctx, id)
}

// AddMessage appends a message to a session.
func (s *Store) AddMessage(ctx context.Context, sessionID string, msg *agent.Message) error {
	if sessionID == "" {
		return agent.ErrInvalidSessionID
	}
	if msg == nil {
		return errors.New("session store: message cannot be nil")
	}

	rec, err := s.col.Get(ctx, sessionID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return agent.ErrSessionNotFound
		}
		return err
	}
	var ss storedSession
	if err := persis.Decode(rec, &ss); err != nil {
		return fmt.Errorf("session store: decode for AddMessage: %w", err)
	}

	ss.Messages = append(ss.Messages, *msg)
	ss.UpdatedAt = time.Now().UTC()
	setTitleFromMessage(&ss, msg)

	data, enc, err := persis.Encode(&ss)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        rec.ID,
		Data:      data,
		Encoding:  enc,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: ss.UpdatedAt,
	}); err != nil {
		return err
	}

	s.mu.Lock()
	s.updatedAt[sessionID] = ss.UpdatedAt
	s.sortUserSessions(ss.UserID)
	s.mu.Unlock()
	return nil
}

// GetMessages retrieves all messages for a session, ordered by SequenceID ascending.
func (s *Store) GetMessages(ctx context.Context, sessionID string) ([]agent.Message, error) {
	if sessionID == "" {
		return nil, agent.ErrInvalidSessionID
	}
	ss, err := s.load(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Message, len(ss.Messages))
	copy(out, ss.Messages)
	return out, nil
}

// GetLatestSequenceID returns the highest sequence ID for a session.
func (s *Store) GetLatestSequenceID(ctx context.Context, sessionID string) (int64, error) {
	if sessionID == "" {
		return 0, agent.ErrInvalidSessionID
	}
	ss, err := s.load(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	var max int64
	for _, msg := range ss.Messages {
		if msg.SequenceID > max {
			max = msg.SequenceID
		}
	}
	return max, nil
}

// ListSubSessions returns all sub-sessions for a parent session.
func (s *Store) ListSubSessions(ctx context.Context, parentSessionID string) ([]*agent.Session, error) {
	if parentSessionID == "" {
		return nil, agent.ErrInvalidSessionID
	}
	s.mu.RLock()
	childIDs := make([]string, len(s.byParent[parentSessionID]))
	copy(childIDs, s.byParent[parentSessionID])
	s.mu.RUnlock()

	out := make([]*agent.Session, 0, len(childIDs))
	for _, id := range childIDs {
		sess, err := s.GetSession(ctx, id)
		if err != nil {
			if errors.Is(err, agent.ErrSessionNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func (s *Store) load(ctx context.Context, id string) (*storedSession, error) {
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, agent.ErrSessionNotFound
		}
		return nil, err
	}
	var ss storedSession
	if err := persis.Decode(rec, &ss); err != nil {
		return nil, fmt.Errorf("session store: decode %q: %w", id, err)
	}
	return &ss, nil
}

func (s *Store) deleteLockedCtx(ctx context.Context, id string) error {
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return agent.ErrSessionNotFound
		}
		return err
	}
	var ss storedSession
	if err := persis.Decode(rec, &ss); err != nil {
		return fmt.Errorf("session store: decode for delete: %w", err)
	}

	if err := s.col.Delete(ctx, id); err != nil {
		return err
	}

	delete(s.updatedAt, id)
	s.byUser[ss.UserID] = removeFromSlice(s.byUser[ss.UserID], id)
	if ss.ParentSessionID != "" {
		s.byParent[ss.ParentSessionID] = removeFromSlice(s.byParent[ss.ParentSessionID], id)
		if len(s.byParent[ss.ParentSessionID]) == 0 {
			delete(s.byParent, ss.ParentSessionID)
		}
	}
	return nil
}

func (s *Store) sortUserSessions(userID string) {
	ids := s.byUser[userID]
	sort.Slice(ids, func(i, j int) bool {
		return s.updatedAt[ids[i]].After(s.updatedAt[ids[j]])
	})
}

func (s *Store) enforceMaxLocked(ctx context.Context, userID string) {
	if s.maxPerUser <= 0 {
		return
	}
	ids := s.byUser[userID]

	subSessions := make(map[string]struct{})
	for _, children := range s.byParent {
		for _, childID := range children {
			subSessions[childID] = struct{}{}
		}
	}

	var topLevel []string
	for _, id := range ids {
		if _, isSub := subSessions[id]; !isSub {
			topLevel = append(topLevel, id)
		}
	}
	if len(topLevel) <= s.maxPerUser {
		return
	}

	excess := topLevel[s.maxPerUser:]
	for _, id := range excess {
		children := append([]string{}, s.byParent[id]...)
		for _, childID := range children {
			if err := s.deleteLockedCtx(ctx, childID); err != nil {
				slog.Warn("session store: failed to delete sub-session during cleanup",
					slog.String("session_id", childID),
					slog.String("parent_id", id),
					slog.String("error", err.Error()))
			}
		}
		if err := s.deleteLockedCtx(ctx, id); err != nil {
			slog.Warn("session store: failed to delete session during cleanup",
				slog.String("session_id", id),
				slog.String("error", err.Error()))
		}
	}
}

func fromSession(sess *agent.Session, messages []agent.Message) *storedSession {
	return &storedSession{
		ID:              sess.ID,
		UserID:          sess.UserID,
		DAGName:         sess.DAGName,
		Title:           sess.Title,
		Model:           sess.Model,
		CreatedAt:       sess.CreatedAt,
		UpdatedAt:       sess.UpdatedAt,
		ParentSessionID: sess.ParentSessionID,
		DelegateTask:    sess.DelegateTask,
		Messages:        messages,
	}
}

func validateSession(sess *agent.Session, requireUserID bool) error {
	if sess == nil {
		return errors.New("session store: session cannot be nil")
	}
	if sess.ID == "" {
		return agent.ErrInvalidSessionID
	}
	if containsPathTraversal(sess.ID) {
		return fmt.Errorf("session store: %w: invalid characters", agent.ErrInvalidSessionID)
	}
	if requireUserID && sess.UserID == "" {
		return agent.ErrInvalidUserID
	}
	if requireUserID && containsPathTraversal(sess.UserID) {
		return fmt.Errorf("session store: %w: invalid characters", agent.ErrInvalidUserID)
	}
	return nil
}

func containsPathTraversal(id string) bool {
	return strings.ContainsAny(id, `/\`) || strings.Contains(id, "..")
}

func setTitleFromMessage(ss *storedSession, msg *agent.Message) {
	if ss.Title == "" && msg.Type == agent.MessageTypeUser && msg.Content != "" {
		ss.Title = truncateTitle(msg.Content)
	}
}

func truncateTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= maxTitleLength {
		return title
	}
	if maxTitleLength < 3 {
		return string(runes[:maxTitleLength])
	}
	return string(runes[:maxTitleLength-3]) + "..."
}

func removeFromSlice(slice []string, target string) []string {
	for i, v := range slice {
		if v == target {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}
