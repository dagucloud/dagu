// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

var _ agent.SessionStore = (*sessionStore)(nil)

const maxAgentSessionTitleLength = 50

type sessionStore struct{ store *Store }

type agentSessionStorage struct {
	ID              string          `json:"id"`
	UserID          string          `json:"user_id"`
	DAGName         string          `json:"dag_name,omitempty"`
	Title           string          `json:"title,omitempty"`
	Model           string          `json:"model,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ParentSessionID string          `json:"parent_session_id,omitempty"`
	DelegateTask    string          `json:"delegate_task,omitempty"`
	Messages        []agent.Message `json:"messages,omitempty"`
}

// Sessions returns the agent session store.
func (s *Store) Sessions() agent.SessionStore {
	return &sessionStore{store: s}
}

func (s *sessionStore) CreateSession(ctx context.Context, sess *agent.Session) error {
	if err := validateAgentSession(sess, true); err != nil {
		return err
	}
	idString, id, err := ensureUUIDv7String(sess.ID)
	if err != nil {
		return agent.ErrInvalidSessionID
	}
	sess.ID = idString
	parentID, err := nullUUIDv7(sess.ParentSessionID)
	if err != nil {
		return agent.ErrInvalidSessionID
	}
	data, err := json.Marshal(sessionStorageFromSession(sess, nil))
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	err = s.store.queries.CreateAgentSession(ctx, db.CreateAgentSessionParams{
		ID:              id,
		UserID:          sess.UserID,
		DagName:         sess.DAGName,
		Title:           sess.Title,
		Model:           sess.Model,
		ParentSessionID: parentID,
		DelegateTask:    sess.DelegateTask,
		Data:            data,
		CreatedAt:       timestamptz(sess.CreatedAt),
		UpdatedAt:       timestamptz(sess.UpdatedAt),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("postgres session store: session %s already exists", sess.ID)
		}
		return err
	}
	return nil
}

func (s *sessionStore) GetSession(ctx context.Context, id string) (*agent.Session, error) {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return nil, agent.ErrInvalidSessionID
	}
	row, err := s.store.queries.GetAgentSession(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, agent.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return sessionFromRow(row)
}

func (s *sessionStore) ListSessions(ctx context.Context, userID string) ([]*agent.Session, error) {
	if userID == "" {
		return nil, agent.ErrInvalidUserID
	}
	rows, err := s.store.queries.ListAgentSessionsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return sessionsFromRows(rows)
}

func (s *sessionStore) UpdateSession(ctx context.Context, sess *agent.Session) error {
	if err := validateAgentSession(sess, true); err != nil {
		return err
	}
	id, err := parseUUIDv7(sess.ID)
	if err != nil {
		return agent.ErrInvalidSessionID
	}
	parentID, err := nullUUIDv7(sess.ParentSessionID)
	if err != nil {
		return agent.ErrInvalidSessionID
	}
	data, err := json.Marshal(sessionStorageFromSession(sess, nil))
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	rows, err := s.store.queries.UpdateAgentSession(ctx, db.UpdateAgentSessionParams{
		ID:              id,
		UserID:          sess.UserID,
		DagName:         sess.DAGName,
		Title:           sess.Title,
		Model:           sess.Model,
		ParentSessionID: parentID,
		DelegateTask:    sess.DelegateTask,
		Data:            data,
		UpdatedAt:       timestamptz(sess.UpdatedAt),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return agent.ErrSessionNotFound
	}
	return nil
}

func (s *sessionStore) DeleteSession(ctx context.Context, id string) error {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return agent.ErrInvalidSessionID
	}
	rows, err := s.store.queries.DeleteAgentSession(ctx, uid)
	if err != nil {
		return err
	}
	if rows == 0 {
		return agent.ErrSessionNotFound
	}
	return nil
}

func (s *sessionStore) AddMessage(ctx context.Context, sessionID string, msg *agent.Message) error {
	if sessionID == "" {
		return agent.ErrInvalidSessionID
	}
	if msg == nil {
		return errors.New("postgres session store: message cannot be nil")
	}
	sessionUUID, err := parseUUIDv7(sessionID)
	if err != nil {
		return agent.ErrInvalidSessionID
	}
	msgIDString, msgUUID, err := ensureUUIDv7String(msg.ID)
	if err != nil {
		msgIDString, msgUUID, err = newUUIDv7String()
		if err != nil {
			return fmt.Errorf("generate message id: %w", err)
		}
	}
	msg.ID = msgIDString
	msg.SessionID = sessionID
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	return s.store.withTx(ctx, func(q *db.Queries) error {
		row, err := q.GetAgentSession(ctx, sessionUUID)
		if errors.Is(err, pgx.ErrNoRows) {
			return agent.ErrSessionNotFound
		}
		if err != nil {
			return err
		}
		if err := q.AddAgentSessionMessage(ctx, db.AddAgentSessionMessageParams{
			ID:          msgUUID,
			SessionID:   sessionUUID,
			MessageType: string(msg.Type),
			SequenceID:  msg.SequenceID,
			CreatedAt:   timestamptz(msg.CreatedAt),
			Data:        data,
		}); err != nil {
			return err
		}

		updatedAt := time.Now().UTC()
		sess, err := sessionFromRow(row)
		if err != nil {
			return err
		}
		if sess.Title == "" && msg.Type == agent.MessageTypeUser && msg.Content != "" {
			sess.Title = truncateAgentSessionTitle(msg.Content)
		}
		sess.UpdatedAt = updatedAt
		parentID, err := nullUUIDv7(sess.ParentSessionID)
		if err != nil {
			return err
		}
		sessionData, err := json.Marshal(sessionStorageFromSession(sess, nil))
		if err != nil {
			return fmt.Errorf("marshal session: %w", err)
		}
		rows, err := q.UpdateAgentSession(ctx, db.UpdateAgentSessionParams{
			ID:              sessionUUID,
			UserID:          sess.UserID,
			DagName:         sess.DAGName,
			Title:           sess.Title,
			Model:           sess.Model,
			ParentSessionID: parentID,
			DelegateTask:    sess.DelegateTask,
			Data:            sessionData,
			UpdatedAt:       timestamptz(updatedAt),
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return agent.ErrSessionNotFound
		}
		return nil
	})
}

func (s *sessionStore) GetMessages(ctx context.Context, sessionID string) ([]agent.Message, error) {
	uid, err := parseUUIDv7(sessionID)
	if err != nil {
		return nil, agent.ErrInvalidSessionID
	}
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	rows, err := s.store.queries.ListAgentSessionMessages(ctx, uid)
	if err != nil {
		return nil, err
	}
	messages := make([]agent.Message, 0, len(rows))
	for _, row := range rows {
		var msg agent.Message
		if err := json.Unmarshal(row.Data, &msg); err != nil {
			return nil, fmt.Errorf("unmarshal message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func (s *sessionStore) GetLatestSequenceID(ctx context.Context, sessionID string) (int64, error) {
	uid, err := parseUUIDv7(sessionID)
	if err != nil {
		return 0, agent.ErrInvalidSessionID
	}
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		return 0, err
	}
	return s.store.queries.GetLatestAgentSessionSequenceID(ctx, uid)
}

func (s *sessionStore) ListSubSessions(ctx context.Context, parentSessionID string) ([]*agent.Session, error) {
	parentID, err := parseUUIDv7(parentSessionID)
	if err != nil {
		return nil, agent.ErrInvalidSessionID
	}
	rows, err := s.store.queries.ListAgentSubSessions(ctx, uuid.NullUUID{UUID: parentID, Valid: true})
	if err != nil {
		return nil, err
	}
	return sessionsFromRows(rows)
}

func validateAgentSession(sess *agent.Session, requireUserID bool) error {
	if sess == nil {
		return errors.New("postgres session store: session cannot be nil")
	}
	if sess.ID == "" {
		return agent.ErrInvalidSessionID
	}
	if requireUserID && sess.UserID == "" {
		return agent.ErrInvalidUserID
	}
	return nil
}

func nullUUIDv7(value string) (uuid.NullUUID, error) {
	if value == "" {
		return uuid.NullUUID{}, nil
	}
	id, err := parseUUIDv7(value)
	if err != nil {
		return uuid.NullUUID{}, err
	}
	return uuid.NullUUID{UUID: id, Valid: true}, nil
}

func sessionFromRow(row db.DaguAgentSession) (*agent.Session, error) {
	var stored agentSessionStorage
	if err := json.Unmarshal(row.Data, &stored); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	sess := stored.toSession()
	sess.ID = row.ID.String()
	sess.UserID = row.UserID
	sess.DAGName = row.DagName.String
	sess.Title = row.Title.String
	sess.Model = row.Model.String
	if row.ParentSessionID.Valid {
		sess.ParentSessionID = row.ParentSessionID.UUID.String()
	}
	sess.DelegateTask = row.DelegateTask.String
	sess.CreatedAt = timeFromTimestamptz(row.CreatedAt)
	sess.UpdatedAt = timeFromTimestamptz(row.UpdatedAt)
	return sess, nil
}

func sessionsFromRows(rows []db.DaguAgentSession) ([]*agent.Session, error) {
	sessions := make([]*agent.Session, 0, len(rows))
	for _, row := range rows {
		sess, err := sessionFromRow(row)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

func sessionStorageFromSession(sess *agent.Session, messages []agent.Message) *agentSessionStorage {
	return &agentSessionStorage{
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

func (s *agentSessionStorage) toSession() *agent.Session {
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

func truncateAgentSessionTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= maxAgentSessionTitleLength {
		return title
	}
	if maxAgentSessionTitleLength < 3 {
		return string(runes[:maxAgentSessionTitleLength])
	}
	return string(runes[:maxAgentSessionTitleLength-3]) + "..."
}
