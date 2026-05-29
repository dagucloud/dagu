// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

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

var _ agent.SessionStore = (*SessionStore)(nil)

const sessionMaxTitleLength = 50

// SessionStore implements [agent.SessionStore] over a [persis.Collection].
// Records are keyed by hierarchical ID "{userID}/{sessionID}", which the
// file backend maps to "{baseDir}/{userID}/{sessionID}.json". Four
// in-memory indices are rebuilt from the collection on startup and kept
// in sync under mu.
type SessionStore struct {
	col        persis.Collection
	maxPerUser int

	mu        sync.RWMutex
	byID      map[string]string    // sessionID → userID
	byUser    map[string][]string  // userID → []sessionID sorted by UpdatedAt desc
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

// SessionOption configures a SessionStore.
type SessionOption func(*SessionStore)

// WithMaxPerUser sets the per-user session cap. When a user exceeds the cap
// the oldest top-level sessions are purged together with their sub-sessions.
// 0 or negative disables the cap.
func WithMaxPerUser(n int) SessionOption {
	return func(s *SessionStore) { s.maxPerUser = n }
}

// NewSessionStore creates a SessionStore backed by col.
func NewSessionStore(col persis.Collection, opts ...SessionOption) (*SessionStore, error) {
	s := &SessionStore{
		col:       col,
		byID:      make(map[string]string),
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

func (s *SessionStore) rebuildIndex(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recs {
		var ss storedSession
		if err := persis.Decode(rec, &ss); err != nil {
			continue
		}
		if ss.ID == "" || ss.UserID == "" {
			continue
		}
		s.byID[ss.ID] = ss.UserID
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

// CreateSession creates a new session. Returns an error if a session with
// the same ID already exists, regardless of UserID.
func (s *SessionStore) CreateSession(ctx context.Context, sess *agent.Session) error {
	if err := validateSessionInput(sess, true); err != nil {
		return err
	}

	ss := sessionFromSession(sess, nil)
	data, err := persis.Encode(ss)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byID[sess.ID]; exists {
		return fmt.Errorf("session store: session %q already exists", sess.ID)
	}
	collID := sessionCollectionID(sess.UserID, sess.ID)
	switch _, err := s.col.Get(ctx, collID); {
	case err == nil:
		return fmt.Errorf("session store: session %q already exists", sess.ID)
	case errors.Is(err, persis.ErrNotFound):
		// proceed
	default:
		return fmt.Errorf("session store: precheck: %w", err)
	}

	if err := s.col.Put(ctx, &persis.Record{
		ID:        collID,
		Data:      data,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("session store: create: %w", err)
	}

	s.byID[sess.ID] = sess.UserID
	s.byUser[sess.UserID] = append(s.byUser[sess.UserID], sess.ID)
	s.updatedAt[sess.ID] = sess.UpdatedAt
	if sess.ParentSessionID != "" {
		s.byParent[sess.ParentSessionID] = append(s.byParent[sess.ParentSessionID], sess.ID)
	}
	s.sortUserSessions(sess.UserID)
	s.enforceMaxLocked(ctx, sess.UserID)
	return nil
}

// GetSession retrieves a session by ID. Messages are not included; use
// GetMessages for those.
func (s *SessionStore) GetSession(ctx context.Context, id string) (*agent.Session, error) {
	if id == "" {
		return nil, agent.ErrInvalidSessionID
	}
	ss, err := s.loadSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return ss.toSession(), nil
}

// ListSessions returns all sessions for a user, sorted by UpdatedAt descending.
func (s *SessionStore) ListSessions(ctx context.Context, userID string) ([]*agent.Session, error) {
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

// UpdateSession updates session metadata (Title, UpdatedAt). All other
// fields on the input are ignored.
func (s *SessionStore) UpdateSession(ctx context.Context, sess *agent.Session) error {
	if err := validateSessionInput(sess, false); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	collID, ok := s.collectionIDLocked(sess.ID)
	if !ok {
		return agent.ErrSessionNotFound
	}
	rec, err := s.col.Get(ctx, collID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return agent.ErrSessionNotFound
		}
		return fmt.Errorf("session store: get for update: %w", err)
	}
	var existing storedSession
	if err := persis.Decode(rec, &existing); err != nil {
		return fmt.Errorf("session store: decode for update: %w", err)
	}

	existing.Title = sess.Title
	existing.UpdatedAt = sess.UpdatedAt

	data, err := persis.Encode(&existing)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        collID,
		Data:      data,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("session store: update: %w", err)
	}

	s.updatedAt[sess.ID] = sess.UpdatedAt
	s.sortUserSessions(existing.UserID)
	return nil
}

// DeleteSession removes a session and its messages. Sub-sessions are
// NOT deleted; cleanup of orphan sub-sessions is the caller's responsibility.
func (s *SessionStore) DeleteSession(ctx context.Context, id string) error {
	if id == "" {
		return agent.ErrInvalidSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(ctx, id)
}

// AddMessage appends a message to a session and updates the session's UpdatedAt.
func (s *SessionStore) AddMessage(ctx context.Context, sessionID string, msg *agent.Message) error {
	if sessionID == "" {
		return agent.ErrInvalidSessionID
	}
	if msg == nil {
		return errors.New("session store: message cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	collID, ok := s.collectionIDLocked(sessionID)
	if !ok {
		return agent.ErrSessionNotFound
	}
	rec, err := s.col.Get(ctx, collID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return agent.ErrSessionNotFound
		}
		return fmt.Errorf("session store: get for add-message: %w", err)
	}
	var ss storedSession
	if err := persis.Decode(rec, &ss); err != nil {
		return fmt.Errorf("session store: decode for add-message: %w", err)
	}

	ss.Messages = append(ss.Messages, *msg)
	ss.UpdatedAt = time.Now().UTC()
	sessionSetTitleFromMessage(&ss, msg)

	data, err := persis.Encode(&ss)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        collID,
		Data:      data,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: ss.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("session store: add-message: %w", err)
	}

	s.updatedAt[sessionID] = ss.UpdatedAt
	s.sortUserSessions(ss.UserID)
	return nil
}

// GetMessages returns the messages stored on a session in insertion order.
func (s *SessionStore) GetMessages(ctx context.Context, sessionID string) ([]agent.Message, error) {
	if sessionID == "" {
		return nil, agent.ErrInvalidSessionID
	}
	ss, err := s.loadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Message, len(ss.Messages))
	copy(out, ss.Messages)
	return out, nil
}

// GetLatestSequenceID returns the highest SequenceID across messages on a
// session, or 0 when the session has no messages.
func (s *SessionStore) GetLatestSequenceID(ctx context.Context, sessionID string) (int64, error) {
	if sessionID == "" {
		return 0, agent.ErrInvalidSessionID
	}
	ss, err := s.loadSession(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	var maxSeq int64
	for _, msg := range ss.Messages {
		if msg.SequenceID > maxSeq {
			maxSeq = msg.SequenceID
		}
	}
	return maxSeq, nil
}

// ListSubSessions returns all sub-sessions whose ParentSessionID matches.
func (s *SessionStore) ListSubSessions(ctx context.Context, parentSessionID string) ([]*agent.Session, error) {
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

// sessionCollectionID returns the hierarchical Collection key for a session.
func sessionCollectionID(userID, sessionID string) string {
	return userID + "/" + sessionID
}

// collectionIDLocked returns the Collection key for a session and whether
// the session is known. Caller must hold s.mu (read or write).
func (s *SessionStore) collectionIDLocked(sessionID string) (string, bool) {
	userID, ok := s.byID[sessionID]
	if !ok {
		return "", false
	}
	return sessionCollectionID(userID, sessionID), true
}

func (s *SessionStore) loadSession(ctx context.Context, id string) (*storedSession, error) {
	s.mu.RLock()
	collID, ok := s.collectionIDLocked(id)
	s.mu.RUnlock()
	if !ok {
		return nil, agent.ErrSessionNotFound
	}

	rec, err := s.col.Get(ctx, collID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, agent.ErrSessionNotFound
		}
		return nil, fmt.Errorf("session store: get %q: %w", id, err)
	}
	var ss storedSession
	if err := persis.Decode(rec, &ss); err != nil {
		return nil, fmt.Errorf("session store: decode %q: %w", id, err)
	}
	return &ss, nil
}

// deleteLocked removes a session by id while holding s.mu for write.
func (s *SessionStore) deleteLocked(ctx context.Context, id string) error {
	userID, ok := s.byID[id]
	if !ok {
		return agent.ErrSessionNotFound
	}
	collID := sessionCollectionID(userID, id)
	rec, err := s.col.Get(ctx, collID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			s.removeFromIndexes(id, userID, "")
			return agent.ErrSessionNotFound
		}
		return fmt.Errorf("session store: get for delete: %w", err)
	}
	var ss storedSession
	if err := persis.Decode(rec, &ss); err != nil {
		return fmt.Errorf("session store: decode for delete: %w", err)
	}

	if err := s.col.Delete(ctx, collID); err != nil {
		return fmt.Errorf("session store: delete: %w", err)
	}
	s.removeFromIndexes(id, ss.UserID, ss.ParentSessionID)
	return nil
}

func (s *SessionStore) removeFromIndexes(id, userID, parentID string) {
	delete(s.byID, id)
	delete(s.updatedAt, id)
	if userID != "" {
		s.byUser[userID] = sessionRemoveFromSlice(s.byUser[userID], id)
		if len(s.byUser[userID]) == 0 {
			delete(s.byUser, userID)
		}
	}
	if parentID != "" {
		s.byParent[parentID] = sessionRemoveFromSlice(s.byParent[parentID], id)
		if len(s.byParent[parentID]) == 0 {
			delete(s.byParent, parentID)
		}
	}
}

func (s *SessionStore) sortUserSessions(userID string) {
	ids := s.byUser[userID]
	sort.Slice(ids, func(i, j int) bool {
		return s.updatedAt[ids[i]].After(s.updatedAt[ids[j]])
	})
}

func (s *SessionStore) enforceMaxLocked(ctx context.Context, userID string) {
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
			if err := s.deleteLocked(ctx, childID); err != nil {
				slog.Warn("session store: failed to delete sub-session during cleanup",
					slog.String("session_id", childID),
					slog.String("parent_id", id),
					slog.String("error", err.Error()))
			}
		}
		if err := s.deleteLocked(ctx, id); err != nil {
			slog.Warn("session store: failed to delete session during cleanup",
				slog.String("session_id", id),
				slog.String("error", err.Error()))
		}
	}
}

func sessionFromSession(sess *agent.Session, messages []agent.Message) *storedSession {
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

func validateSessionInput(sess *agent.Session, requireUserID bool) error {
	if sess == nil {
		return errors.New("session store: session cannot be nil")
	}
	if sess.ID == "" {
		return agent.ErrInvalidSessionID
	}
	if sessionContainsPathTraversal(sess.ID) {
		return fmt.Errorf("session store: %w: invalid characters", agent.ErrInvalidSessionID)
	}
	if requireUserID && sess.UserID == "" {
		return agent.ErrInvalidUserID
	}
	if requireUserID && sessionContainsPathTraversal(sess.UserID) {
		return fmt.Errorf("session store: %w: invalid characters", agent.ErrInvalidUserID)
	}
	return nil
}

func sessionContainsPathTraversal(id string) bool {
	return strings.ContainsAny(id, `/\`) || strings.Contains(id, "..")
}

func sessionSetTitleFromMessage(ss *storedSession, msg *agent.Message) {
	if ss.Title == "" && msg.Type == agent.MessageTypeUser && msg.Content != "" {
		ss.Title = sessionTruncateTitle(msg.Content)
	}
}

func sessionTruncateTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= sessionMaxTitleLength {
		return title
	}
	if sessionMaxTitleLength < 3 {
		return string(runes[:sessionMaxTitleLength])
	}
	return string(runes[:sessionMaxTitleLength-3]) + "..."
}

func sessionRemoveFromSlice(slice []string, target string) []string {
	for i, v := range slice {
		if v == target {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}
