// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/google/uuid"
)

const (
	defaultMaxTopicsPerConnection = 20
	defaultWriteBufferSize        = 64 * 1024
	defaultSlowClientTimeout      = 30 * time.Second
)

var (
	ErrUnknownSession           = errors.New("unknown session")
	ErrTooManyTopics            = errors.New("too many topics for one connection")
	ErrConflictingTopicMutation = errors.New("conflicting topic mutation")
)

// StreamConfig controls multiplexed SSE sessions.
type StreamConfig struct {
	MaxTopicsPerConnection int
	MaxClients             int
	HeartbeatInterval      time.Duration
	WriteBufferSize        int
	SlowClientTimeout      time.Duration
}

// TopicAuthorizer validates whether the current request may subscribe to a topic.
type TopicAuthorizer func(ctx context.Context, identifier string) error

// TopicMutationError reports a partial subscribe failure.
type TopicMutationError struct {
	Topic   string `json:"topic"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StreamControlEvent is sent before any data messages on a stream.
type StreamControlEvent struct {
	SessionID  string               `json:"sessionID"`
	Subscribed []string             `json:"subscribed"`
	Errors     []TopicMutationError `json:"errors,omitempty"`
}

// TopicMutationResponse reports the post-mutation subscription set.
type TopicMutationResponse struct {
	Subscribed []string             `json:"subscribed"`
	Errors     []TopicMutationError `json:"errors,omitempty"`
}

type messageEnvelope struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

type mutationResult struct {
	response   TopicMutationResponse
	added      []*multiplexTopic
	statusCode int
}

type createSessionResult struct {
	session *streamSession
	control StreamControlEvent
	topics  []*multiplexTopic
}

type queuedMessage struct {
	topic   string
	eventID uint64
	data    []byte
	size    int
}

// Multiplexer manages one multiplexed SSE session per browser tab.
type Multiplexer struct {
	mu                  sync.RWMutex
	sessions            map[string]*streamSession
	topics              map[string]*multiplexTopic
	fetchers            map[TopicType]FetchFunc
	refreshModes        map[TopicType]TopicRefreshMode
	publishOnWake       map[TopicType]bool
	authorizers         map[TopicType]TopicAuthorizer
	maxClients          int
	maxTopicsPerConn    int
	heartbeatInterval   time.Duration
	writeBufferSize     int
	slowClientTimeout   time.Duration
	watcherBaseInterval time.Duration
	watcherMaxInterval  time.Duration
	metrics             *Metrics
	ctx                 context.Context
	cancel              context.CancelFunc
	eventIDMu           sync.Mutex
	nextEventID         uint64
}

// NewMultiplexer creates a multiplexed SSE manager.
func NewMultiplexer(cfg StreamConfig, metrics *Metrics) *Multiplexer {
	ctx, cancel := context.WithCancel(context.Background())

	if cfg.MaxClients <= 0 {
		cfg.MaxClients = defaultMaxClients
	}
	if cfg.MaxTopicsPerConnection <= 0 {
		cfg.MaxTopicsPerConnection = defaultMaxTopicsPerConnection
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = heartbeatInterval
	}
	if cfg.WriteBufferSize <= 0 {
		cfg.WriteBufferSize = defaultWriteBufferSize
	}
	if cfg.SlowClientTimeout <= 0 {
		cfg.SlowClientTimeout = defaultSlowClientTimeout
	}

	return &Multiplexer{
		sessions:            make(map[string]*streamSession),
		topics:              make(map[string]*multiplexTopic),
		fetchers:            make(map[TopicType]FetchFunc),
		refreshModes:        make(map[TopicType]TopicRefreshMode),
		publishOnWake:       make(map[TopicType]bool),
		authorizers:         make(map[TopicType]TopicAuthorizer),
		maxClients:          cfg.MaxClients,
		maxTopicsPerConn:    cfg.MaxTopicsPerConnection,
		heartbeatInterval:   cfg.HeartbeatInterval,
		writeBufferSize:     cfg.WriteBufferSize,
		slowClientTimeout:   cfg.SlowClientTimeout,
		watcherBaseInterval: defaultBaseInterval,
		watcherMaxInterval:  defaultMaxInterval,
		metrics:             metrics,
		ctx:                 ctx,
		cancel:              cancel,
	}
}

// RegisterFetcher registers a data fetcher for multiplexed topics.
func (m *Multiplexer) RegisterFetcher(topicType TopicType, fetcher FetchFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fetchers[topicType] = fetcher
}

// SetRefreshMode configures how a topic stays fresh after its initial snapshot.
func (m *Multiplexer) SetRefreshMode(topicType TopicType, mode TopicRefreshMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mode == "" {
		delete(m.refreshModes, topicType)
		return
	}
	m.refreshModes[topicType] = mode
}

// SetPublishOnWake forces an event to be emitted for explicit wakeups even when
// the fetched payload hash is unchanged. This is useful for invalidation-only
// consumers whose canonical data may span multiple pages beyond the topic payload.
func (m *Multiplexer) SetPublishOnWake(topicType TopicType, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !enabled {
		delete(m.publishOnWake, topicType)
		return
	}
	m.publishOnWake[topicType] = true
}

// RegisterAuthorizer registers an optional topic-specific authorizer.
func (m *Multiplexer) RegisterAuthorizer(topicType TopicType, authorizer TopicAuthorizer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authorizers[topicType] = authorizer
}

// WakeTopic requests an immediate refetch for an active exact topic.
func (m *Multiplexer) WakeTopic(topicType TopicType, identifier string) {
	parsed, err := ParseTopic(string(topicType) + ":" + identifier)
	if err != nil {
		return
	}
	m.wakeTopicKey(parsed.Key)
}

// WakeTopicType requests an immediate refetch for all active topics of the given type.
func (m *Multiplexer) WakeTopicType(topicType TopicType) {
	m.mu.RLock()
	topics := make([]*multiplexTopic, 0, len(m.topics))
	for _, topic := range m.topics {
		if topic == nil || topic.topicType != topicType {
			continue
		}
		topics = append(topics, topic)
	}
	m.mu.RUnlock()

	for _, topic := range topics {
		topic.requestPoll()
	}
}

func (m *Multiplexer) wakeTopicKey(key string) {
	m.mu.RLock()
	topic := m.topics[key]
	m.mu.RUnlock()
	if topic == nil {
		return
	}
	topic.requestPoll()
}

// Shutdown stops all multiplexed sessions and topic watchers.
func (m *Multiplexer) Shutdown() {
	m.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, session := range m.sessions {
		session.close()
	}
	for _, topic := range m.topics {
		topic.stop()
	}

	m.sessions = make(map[string]*streamSession)
	m.topics = make(map[string]*multiplexTopic)
}

func (m *Multiplexer) Context() context.Context {
	return m.ctx
}

// createSession creates a multiplexed SSE session and applies the initial topic set.
func (m *Multiplexer) createSession(ctx context.Context, w http.ResponseWriter, requested []string, lastEventID uint64) (createSessionResult, error) {
	session, err := newStreamSession(w, m, context.WithoutCancel(ctx))
	if err != nil {
		return createSessionResult{}, err
	}

	m.mu.Lock()
	if len(m.sessions) >= m.maxClients {
		m.mu.Unlock()
		return createSessionResult{}, fmt.Errorf("max clients reached (%d)", m.maxClients)
	}
	m.sessions[session.id] = session
	m.mu.Unlock()

	if m.metrics != nil {
		m.metrics.MultiplexSessionConnected()
	}

	result, err := m.applyMutation(ctx, session, requested, nil)
	if err != nil {
		m.removeSession(session)
		return createSessionResult{}, err
	}

	control := StreamControlEvent{
		SessionID:  session.id,
		Subscribed: result.response.Subscribed,
		Errors:     result.response.Errors,
	}
	if m.metrics != nil {
		m.metrics.ObserveTopicsPerSession(len(control.Subscribed))
	}

	session.lastSeenEventID = lastEventID
	return createSessionResult{
		session: session,
		control: control,
		topics:  result.added,
	}, nil
}

// mutateSession applies add/remove topic mutations to an existing session.
func (m *Multiplexer) mutateSession(ctx context.Context, sessionID string, add, remove []string) (mutationResult, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return mutationResult{}, err
	}

	result, err := m.applyMutation(ctx, session, add, remove)
	if err != nil {
		return mutationResult{}, err
	}

	if m.metrics != nil {
		m.metrics.ObserveTopicsPerSession(len(result.response.Subscribed))
	}

	return result, nil
}

func (m *Multiplexer) getSession(sessionID string) (*streamSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session := m.sessions[sessionID]
	if session == nil || session.isClosed() {
		if m.metrics != nil {
			m.metrics.UnknownSessionMutation()
		}
		return nil, ErrUnknownSession
	}
	return session, nil
}

func (m *Multiplexer) applyMutation(ctx context.Context, session *streamSession, add, remove []string) (mutationResult, error) {
	addedParsed, err := parseTopicList(add)
	if err != nil {
		return mutationResult{}, err
	}
	removedParsed, err := parseTopicList(remove)
	if err != nil {
		return mutationResult{}, err
	}

	removeSet := make(map[string]struct{}, len(removedParsed))
	for _, parsed := range removedParsed {
		removeSet[parsed.Key] = struct{}{}
	}
	for _, parsed := range addedParsed {
		if _, exists := removeSet[parsed.Key]; exists {
			return mutationResult{}, fmt.Errorf(
				"%w: topic %q cannot be added and removed in the same request",
				ErrConflictingTopicMutation,
				parsed.Key,
			)
		}
	}

	// TODO: Extract add-topic classification into a helper when we can do a
	// broader cleanup without mixing behavior changes into this regression fix.
	mutationErrors := make([]TopicMutationError, 0)
	authorizedAdds := make([]ParsedTopic, 0, len(addedParsed))
	for _, parsed := range addedParsed {
		authorizer := m.getAuthorizer(parsed.Type)
		if authorizer == nil {
			authorizedAdds = append(authorizedAdds, parsed)
			continue
		}
		if err := authorizer(ctx, parsed.Identifier); err != nil {
			mutationErrors = append(mutationErrors, TopicMutationError{
				Topic:   parsed.Key,
				Code:    "unauthorized",
				Message: err.Error(),
			})
			continue
		}
		authorizedAdds = append(authorizedAdds, parsed)
	}

	supportedAdds := make([]ParsedTopic, 0, len(authorizedAdds))
	for _, parsed := range authorizedAdds {
		if m.hasFetcher(parsed.Type) {
			supportedAdds = append(supportedAdds, parsed)
			continue
		}
		mutationErrors = append(mutationErrors, TopicMutationError{
			Topic:   parsed.Key,
			Code:    "unsupported_topic",
			Message: fmt.Sprintf("topic type %q is not supported by this server", parsed.Type),
		})
	}

	resolvedAdds, createdTopics, err := m.resolveTopicsForMutation(supportedAdds)
	if err != nil {
		return mutationResult{}, err
	}
	resolvedByKey := make(map[string]*multiplexTopic, len(supportedAdds))
	for idx, parsed := range supportedAdds {
		resolvedByKey[parsed.Key] = resolvedAdds[idx]
	}

	session.mutationMu.Lock()
	defer session.mutationMu.Unlock()

	if session.isClosed() {
		m.cleanupResolvedTopics(createdTopics, nil)
		return mutationResult{}, ErrUnknownSession
	}

	currentTopics := session.topicKeys()
	currentSet := make(map[string]struct{}, len(currentTopics))
	for _, topicKey := range currentTopics {
		currentSet[topicKey] = struct{}{}
	}

	finalCount := len(currentSet)
	for key := range removeSet {
		if _, exists := currentSet[key]; exists {
			finalCount--
		}
	}

	addsToApply := make([]ParsedTopic, 0, len(supportedAdds))
	for _, parsed := range supportedAdds {
		if _, exists := currentSet[parsed.Key]; exists {
			continue
		}
		addsToApply = append(addsToApply, parsed)
		finalCount++
	}
	if finalCount > m.maxTopicsPerConn {
		m.cleanupResolvedTopics(createdTopics, nil)
		return mutationResult{}, ErrTooManyTopics
	}

	for _, parsed := range removedParsed {
		m.unsubscribeTopic(session, parsed.Key)
	}

	addedTopics := make([]*multiplexTopic, 0, len(addsToApply))
	addedTopicKeys := make(map[string]struct{}, len(addsToApply))
	for _, parsed := range addsToApply {
		topic, err := m.attachTopicToSession(session, parsed, resolvedByKey[parsed.Key])
		if err != nil {
			m.cleanupResolvedTopics(createdTopics, addedTopicKeys)
			return mutationResult{}, err
		}
		if topic == nil {
			continue
		}

		addedTopics = append(addedTopics, topic)
		addedTopicKeys[topic.key] = struct{}{}
		if m.metrics != nil {
			m.metrics.TopicMutation("subscribe", string(parsed.Type))
		}
	}
	m.cleanupResolvedTopics(createdTopics, addedTopicKeys)

	// TODO: Split partial-mutation HTTP semantics in a dedicated API/UI cleanup.
	// Unsupported topics currently share 403 with authorization failures because
	// the frontend already treats 403 as a partial-success response.
	statusCode := http.StatusOK
	if len(mutationErrors) > 0 {
		statusCode = http.StatusForbidden
	}

	return mutationResult{
		response: TopicMutationResponse{
			Subscribed: session.topicKeys(),
			Errors:     mutationErrors,
		},
		added:      addedTopics,
		statusCode: statusCode,
	}, nil
}

func (m *Multiplexer) attachTopicToSession(session *streamSession, parsed ParsedTopic, candidate *multiplexTopic) (*multiplexTopic, error) {
	for range 3 {
		topic, err := m.ensureAttachableTopic(parsed, candidate)
		if err != nil {
			return nil, err
		}
		if topic == nil {
			return nil, nil
		}

		if !session.addTopic(topic) {
			return nil, nil
		}
		if topic.addSession(session) {
			return topic, nil
		}

		session.removeTopic(topic.key)
		candidate = nil
	}

	return nil, fmt.Errorf("topic %q could not be attached", parsed.Key)
}

func (m *Multiplexer) ensureAttachableTopic(parsed ParsedTopic, candidate *multiplexTopic) (*multiplexTopic, error) {
	if candidate != nil && !candidate.isRetiring() {
		return candidate, nil
	}

	topic, _, err := m.getOrCreateTopicForMutation(parsed)
	if err != nil {
		return nil, err
	}
	return topic, nil
}

func (m *Multiplexer) getAuthorizer(topicType TopicType) TopicAuthorizer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.authorizers[topicType]
}

func (m *Multiplexer) hasFetcher(topicType TopicType) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fetchers[topicType] != nil
}

func (m *Multiplexer) resolveTopicsForMutation(parsedTopics []ParsedTopic) ([]*multiplexTopic, []*multiplexTopic, error) {
	if len(parsedTopics) == 0 {
		return nil, nil, nil
	}

	resolved := make([]*multiplexTopic, 0, len(parsedTopics))
	created := make([]*multiplexTopic, 0, len(parsedTopics))
	for _, parsed := range parsedTopics {
		topic, wasCreated, err := m.getOrCreateTopicForMutation(parsed)
		if err != nil {
			m.cleanupResolvedTopics(created, nil)
			return nil, nil, err
		}
		resolved = append(resolved, topic)
		if wasCreated {
			created = append(created, topic)
		}
	}

	return resolved, created, nil
}

func (m *Multiplexer) cleanupResolvedTopics(topics []*multiplexTopic, keep map[string]struct{}) {
	for _, topic := range topics {
		if topic == nil {
			continue
		}
		if keep != nil {
			if _, exists := keep[topic.key]; exists {
				continue
			}
		}
		m.retireTopicIfUnused(topic)
	}
}

func (m *Multiplexer) getOrCreateTopicForMutation(parsed ParsedTopic) (*multiplexTopic, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if topic := m.topics[parsed.Key]; topic != nil {
		if topic.isRetiring() {
			delete(m.topics, parsed.Key)
		} else {
			return topic, false, nil
		}
	}

	fetcher := m.fetchers[parsed.Type]
	if fetcher == nil {
		return nil, false, fmt.Errorf("no fetcher registered for topic type: %s", parsed.Type)
	}

	topic := newMultiplexTopic(
		m,
		parsed,
		fetcher,
		m.refreshModeFor(parsed.Type),
		m.shouldPublishOnWake(parsed.Type),
	)
	m.topics[parsed.Key] = topic
	return topic, true, nil
}

func (m *Multiplexer) refreshModeFor(topicType TopicType) TopicRefreshMode {
	if mode, ok := m.refreshModes[topicType]; ok && mode != "" {
		return mode
	}
	return TopicRefreshModePolling
}

func (m *Multiplexer) shouldPublishOnWake(topicType TopicType) bool {
	return m.publishOnWake[topicType]
}

func (m *Multiplexer) retireTopicIfUnused(topic *multiplexTopic) {
	if topic == nil {
		return
	}

	m.mu.Lock()
	current := m.topics[topic.key]
	if current != topic || !topic.markRetiringIfUnused() {
		m.mu.Unlock()
		return
	}
	delete(m.topics, topic.key)
	m.mu.Unlock()

	topic.stop()
}

func (m *Multiplexer) unsubscribeTopic(session *streamSession, topicKey string) {
	topic := session.removeTopic(topicKey)
	if topic == nil {
		return
	}

	topic.removeSession(session)
	if m.metrics != nil {
		m.metrics.TopicMutation("unsubscribe", string(topic.topicType))
	}
	if topic.sessionCount() == 0 {
		m.retireTopicIfUnused(topic)
	}
}

func (m *Multiplexer) removeSession(session *streamSession) {
	if session == nil {
		return
	}

	session.mutationMu.Lock()
	topicKeys := session.topicKeys()
	for _, topicKey := range topicKeys {
		m.unsubscribeTopic(session, topicKey)
	}

	session.close()
	session.mutationMu.Unlock()

	m.mu.Lock()
	delete(m.sessions, session.id)
	m.mu.Unlock()

	if m.metrics != nil {
		m.metrics.MultiplexSessionDisconnected()
	}
}

func (m *Multiplexer) nextID() uint64 {
	m.eventIDMu.Lock()
	defer m.eventIDMu.Unlock()
	m.nextEventID++
	return m.nextEventID
}

func parseTopicList(rawTopics []string) ([]ParsedTopic, error) {
	if len(rawTopics) == 0 {
		return nil, nil
	}

	parsed := make([]ParsedTopic, 0, len(rawTopics))
	seen := make(map[string]struct{}, len(rawTopics))
	for _, raw := range rawTopics {
		topic, err := ParseTopic(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[topic.Key]; exists {
			continue
		}
		seen[topic.Key] = struct{}{}
		parsed = append(parsed, topic)
	}

	return parsed, nil
}

func newStreamSession(w http.ResponseWriter, mux *Multiplexer, fetchCtx context.Context) (*streamSession, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, ErrStreamingNotSupported
	}
	if fetchCtx == nil {
		fetchCtx = context.Background()
	}

	return &streamSession{
		id:                uuid.Must(uuid.NewV7()).String(),
		w:                 w,
		flusher:           flusher,
		controller:        http.NewResponseController(w),
		mux:               mux,
		fetchCtx:          fetchCtx,
		topics:            make(map[string]*multiplexTopic),
		ready:             make(chan struct{}, 1),
		heartbeatInterval: mux.heartbeatInterval,
		writeBufferSize:   mux.writeBufferSize,
		slowClientTimeout: mux.slowClientTimeout,
	}, nil
}

type streamSession struct {
	id                string
	w                 http.ResponseWriter
	flusher           http.Flusher
	controller        *http.ResponseController
	mux               *Multiplexer
	fetchCtx          context.Context
	heartbeatInterval time.Duration
	writeBufferSize   int
	slowClientTimeout time.Duration

	mutationMu      sync.Mutex
	mu              sync.Mutex
	closed          bool
	topics          map[string]*multiplexTopic
	queue           []*queuedMessage
	queuedByTopic   map[string]*queuedMessage
	queuedBytes     int
	ready           chan struct{}
	lastSeenEventID uint64
}

func (s *streamSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *streamSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.signalReady()
}

func (s *streamSession) addTopic(topic *multiplexTopic) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if _, exists := s.topics[topic.key]; exists {
		return false
	}
	s.topics[topic.key] = topic
	return true
}

func (s *streamSession) removeTopic(topicKey string) *multiplexTopic {
	s.mu.Lock()
	defer s.mu.Unlock()
	topic := s.topics[topicKey]
	delete(s.topics, topicKey)
	if queued := s.queuedByTopic[topicKey]; queued != nil {
		filtered := s.queue[:0]
		for _, msg := range s.queue {
			if msg.topic == topicKey {
				s.queuedBytes -= msg.size
				continue
			}
			filtered = append(filtered, msg)
		}
		s.queue = filtered
		delete(s.queuedByTopic, topicKey)
	}
	return topic
}

func (s *streamSession) topicKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]string, 0, len(s.topics))
	for topicKey := range s.topics {
		keys = append(keys, topicKey)
	}
	slices.Sort(keys)
	return keys
}

func (s *streamSession) bootstrapTopics(ctx context.Context, lastEventID uint64, topics []*multiplexTopic) {
	for _, topic := range topics {
		if lastEventID > 0 && !topic.changedSince(lastEventID) {
			continue
		}
		if err := topic.sendSnapshot(ctx, s, s.mux.nextID()); err != nil {
			continue
		}
	}
}

func (s *streamSession) enqueueMessage(topic string, eventID uint64, data []byte) bool {
	if len(data) == 0 {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}
	if _, exists := s.topics[topic]; !exists {
		return false
	}
	if s.queuedByTopic == nil {
		s.queuedByTopic = make(map[string]*queuedMessage)
	}

	size := len(data) + 64
	if size > s.writeBufferSize {
		s.closed = true
		if s.mux.metrics != nil {
			s.mux.metrics.BackpressureDisconnect()
		}
		return false
	}

	if existing := s.queuedByTopic[topic]; existing != nil {
		s.queuedBytes -= existing.size
		existing.eventID = eventID
		existing.data = data
		existing.size = size
		s.queuedBytes += size
		for s.queuedBytes > s.writeBufferSize {
			if !s.dropOldestExcept(topic) {
				s.closed = true
				if s.mux.metrics != nil {
					s.mux.metrics.BackpressureDisconnect()
				}
				s.signalReady()
				return false
			}
		}
		s.signalReady()
		return true
	}

	for s.queuedBytes+size > s.writeBufferSize && len(s.queue) > 0 {
		s.dropOldest()
	}
	if s.queuedBytes+size > s.writeBufferSize {
		s.closed = true
		if s.mux.metrics != nil {
			s.mux.metrics.BackpressureDisconnect()
		}
		s.signalReady()
		return false
	}

	msg := &queuedMessage{
		topic:   topic,
		eventID: eventID,
		data:    data,
		size:    size,
	}
	s.queue = append(s.queue, msg)
	s.queuedByTopic[topic] = msg
	s.queuedBytes += size
	s.signalReady()
	return true
}

func (s *streamSession) dropOldest() {
	if len(s.queue) == 0 {
		return
	}
	oldest := s.queue[0]
	s.queue = s.queue[1:]
	delete(s.queuedByTopic, oldest.topic)
	s.queuedBytes -= oldest.size
}

func (s *streamSession) dropOldestExcept(topic string) bool {
	for i, msg := range s.queue {
		if msg.topic == topic {
			continue
		}
		s.queue = append(s.queue[:i], s.queue[i+1:]...)
		delete(s.queuedByTopic, msg.topic)
		s.queuedBytes -= msg.size
		return true
	}
	return false
}

func (s *streamSession) popNext() *queuedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || len(s.queue) == 0 {
		return nil
	}

	msg := s.queue[0]
	s.queue = s.queue[1:]
	delete(s.queuedByTopic, msg.topic)
	s.queuedBytes -= msg.size
	s.lastSeenEventID = msg.eventID
	return msg
}

func (s *streamSession) signalReady() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *streamSession) writeControl(payload StreamControlEvent) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.writeFrame(0, "control", data)
}

func (s *streamSession) writeFrame(eventID uint64, eventType string, data []byte) error {
	var buf bytes.Buffer
	if eventID > 0 {
		if _, err := fmt.Fprintf(&buf, "id: %d\n", eventID); err != nil {
			return err
		}
	}
	if eventType != "" {
		if _, err := fmt.Fprintf(&buf, "event: %s\n", eventType); err != nil {
			return err
		}
	}
	if _, err := buf.WriteString("data: "); err != nil {
		return err
	}
	if _, err := buf.Write(data); err != nil {
		return err
	}
	if _, err := buf.WriteString("\n\n"); err != nil {
		return err
	}

	if s.slowClientTimeout > 0 {
		_ = s.controller.SetWriteDeadline(time.Now().Add(s.slowClientTimeout))
	}
	if _, err := s.w.Write(buf.Bytes()); err != nil {
		if s.mux.metrics != nil {
			s.mux.metrics.BackpressureDisconnect()
		}
		return err
	}
	s.flusher.Flush()
	if s.slowClientTimeout > 0 {
		_ = s.controller.SetWriteDeadline(time.Time{})
	}
	if s.mux.metrics != nil {
		s.mux.metrics.MessageSent(eventType)
	}
	return nil
}

func (s *streamSession) writeHeartbeat() error {
	if s.slowClientTimeout > 0 {
		_ = s.controller.SetWriteDeadline(time.Now().Add(s.slowClientTimeout))
	}
	if _, err := fmt.Fprint(s.w, ": heartbeat\n\n"); err != nil {
		if s.mux.metrics != nil {
			s.mux.metrics.BackpressureDisconnect()
		}
		return err
	}
	s.flusher.Flush()
	if s.slowClientTimeout > 0 {
		_ = s.controller.SetWriteDeadline(time.Time{})
	}
	if s.mux.metrics != nil {
		s.mux.metrics.MessageSent(EventTypeHeartbeat)
	}
	return nil
}

func (s *streamSession) Serve(ctx context.Context) error {
	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if s.isClosed() {
				return nil
			}
			if err := s.writeHeartbeat(); err != nil {
				return err
			}
		case <-s.ready:
			if s.isClosed() {
				return nil
			}
			for {
				msg := s.popNext()
				if msg == nil {
					break
				}
				if err := s.writeFrame(msg.eventID, "message", msg.data); err != nil {
					return err
				}
			}
		}
	}
}

type multiplexTopic struct {
	mux               *Multiplexer
	key               string
	topicType         TopicType
	identifier        string
	fetcher           FetchFunc
	refreshMode       TopicRefreshMode
	publishOnWake     bool
	clientsMu         sync.RWMutex
	sessions          map[*streamSession]struct{}
	retiring          bool
	lastHashBySession map[*streamSession]string
	lastChangeEventID uint64
	stopCh            chan struct{}
	notifyCh          chan struct{}
	wg                sync.WaitGroup
	errorBackoff      backoff.Retrier
	backoffUntil      time.Time
	currentInterval   time.Duration
}

func newMultiplexTopic(
	mux *Multiplexer,
	parsed ParsedTopic,
	fetcher FetchFunc,
	refreshMode TopicRefreshMode,
	publishOnWake bool,
) *multiplexTopic {
	policy := backoff.NewExponentialBackoffPolicy(time.Second)
	policy.MaxInterval = 30 * time.Second

	return &multiplexTopic{
		mux:               mux,
		key:               parsed.Key,
		topicType:         parsed.Type,
		identifier:        parsed.Identifier,
		fetcher:           fetcher,
		refreshMode:       refreshMode,
		publishOnWake:     publishOnWake,
		sessions:          make(map[*streamSession]struct{}),
		lastHashBySession: make(map[*streamSession]string),
		stopCh:            make(chan struct{}),
		notifyCh:          make(chan struct{}, 1),
		errorBackoff:      backoff.NewRetrier(policy),
		currentInterval:   mux.watcherBaseInterval,
	}
}

func (t *multiplexTopic) addSession(session *streamSession) bool {
	t.clientsMu.Lock()
	defer t.clientsMu.Unlock()

	if t.retiring {
		return false
	}
	if _, exists := t.sessions[session]; exists {
		return true
	}
	t.sessions[session] = struct{}{}
	if len(t.sessions) == 1 {
		t.start()
	}
	return true
}

func (t *multiplexTopic) removeSession(session *streamSession) {
	t.clientsMu.Lock()
	defer t.clientsMu.Unlock()
	delete(t.sessions, session)
	delete(t.lastHashBySession, session)
}

func (t *multiplexTopic) sessionCount() int {
	t.clientsMu.RLock()
	defer t.clientsMu.RUnlock()
	return len(t.sessions)
}

func (t *multiplexTopic) isRetiring() bool {
	t.clientsMu.RLock()
	defer t.clientsMu.RUnlock()
	return t.retiring
}

func (t *multiplexTopic) markRetiringIfUnused() bool {
	t.clientsMu.Lock()
	defer t.clientsMu.Unlock()
	if t.retiring || len(t.sessions) > 0 {
		return false
	}
	t.retiring = true
	return true
}

func (t *multiplexTopic) changedSince(lastEventID uint64) bool {
	t.clientsMu.RLock()
	defer t.clientsMu.RUnlock()
	return t.lastChangeEventID > lastEventID
}

func (t *multiplexTopic) start() {
	t.wg.Go(func() {
		timer := time.NewTimer(time.Hour)
		if !timer.Stop() {
			<-timer.C
		}
		defer timer.Stop()

		var timerCh <-chan time.Time
		if t.refreshMode == TopicRefreshModePolling {
			t.resetTimer(timer, &timerCh, 0)
		}

		for {
			select {
			case <-t.mux.ctx.Done():
				return
			case <-t.stopCh:
				return
			case <-timerCh:
				timerCh = nil
				t.poll(false)
				if delay, shouldRetry := t.nextRetryDelay(); shouldRetry {
					t.resetTimer(timer, &timerCh, delay)
					continue
				}
				if t.refreshMode == TopicRefreshModePolling {
					t.resetTimer(timer, &timerCh, t.currentInterval)
				}
			case <-t.notifyCh:
				t.poll(t.publishOnWake)
				if delay, shouldRetry := t.nextRetryDelay(); shouldRetry {
					t.resetTimer(timer, &timerCh, delay)
					continue
				}
				if t.refreshMode == TopicRefreshModePolling {
					t.resetTimer(timer, &timerCh, t.currentInterval)
					continue
				}
				t.stopTimer(timer, &timerCh)
			}
		}
	})
}

func (t *multiplexTopic) stop() {
	select {
	case <-t.stopCh:
	default:
		close(t.stopCh)
	}
	t.wg.Wait()
}

func (t *multiplexTopic) requestPoll() {
	select {
	case t.notifyCh <- struct{}{}:
	default:
	}
}

func (t *multiplexTopic) stopTimer(timer *time.Timer, timerCh *<-chan time.Time) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	*timerCh = nil
}

func (t *multiplexTopic) resetTimer(timer *time.Timer, timerCh *<-chan time.Time, delay time.Duration) {
	t.stopTimer(timer, timerCh)
	timer.Reset(delay)
	*timerCh = timer.C
}

func (t *multiplexTopic) nextRetryDelay() (time.Duration, bool) {
	if t.backoffUntil.IsZero() {
		return 0, false
	}
	delay := max(time.Until(t.backoffUntil), 0)
	return delay, true
}

func (t *multiplexTopic) poll(forcePublish bool) {
	if time.Now().Before(t.backoffUntil) {
		return
	}

	t.clientsMu.RLock()
	sessions := make([]*streamSession, 0, len(t.sessions))
	for session := range t.sessions {
		sessions = append(sessions, session)
	}
	t.clientsMu.RUnlock()

	var totalSuccessFetchDuration time.Duration
	var successCount int
	var firstErr error

	for _, session := range sessions {
		start := time.Now()
		payload, err := t.fetchPayload(session.fetchCtx)
		fetchDuration := time.Since(start)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		totalSuccessFetchDuration += fetchDuration
		successCount++
		if t.mux.metrics != nil {
			t.mux.metrics.RecordFetchDuration(string(t.topicType), fetchDuration)
		}

		hash := computeHash(payload)
		t.clientsMu.Lock()
		if _, ok := t.sessions[session]; !ok {
			t.clientsMu.Unlock()
			continue
		}
		if hash == t.lastHashBySession[session] && !forcePublish {
			t.clientsMu.Unlock()
			continue
		}
		t.lastHashBySession[session] = hash
		eventID := t.mux.nextID()
		t.lastChangeEventID = eventID
		t.clientsMu.Unlock()

		data, err := buildMessageData(t.key, payload)
		if err != nil {
			continue
		}
		session.enqueueMessage(t.key, eventID, data)
	}

	if firstErr != nil && t.mux.metrics != nil {
		t.mux.metrics.FetchError(string(t.topicType))
	}
	if successCount == 0 && firstErr != nil {
		interval, _ := t.errorBackoff.Next(firstErr)
		t.backoffUntil = time.Now().Add(interval)
		return
	}
	if successCount == 0 {
		return
	}

	fetchDuration := totalSuccessFetchDuration / time.Duration(successCount)
	t.backoffUntil = time.Time{}
	t.errorBackoff.Reset()
	t.currentInterval = time.Duration(
		float64(max(t.mux.watcherBaseInterval, min(intervalMultiplier*fetchDuration, t.mux.watcherMaxInterval)))*smoothingFactor +
			float64(t.currentInterval)*(1-smoothingFactor),
	)
}

func (t *multiplexTopic) sendSnapshot(ctx context.Context, session *streamSession, eventID uint64) error {
	payload, err := t.fetchPayload(ctx)
	if err != nil {
		return err
	}
	hash := computeHash(payload)
	t.clientsMu.Lock()
	if _, ok := t.sessions[session]; !ok {
		t.clientsMu.Unlock()
		return nil
	}
	t.lastHashBySession[session] = hash
	if t.lastChangeEventID == 0 {
		t.lastChangeEventID = eventID
	}
	t.clientsMu.Unlock()
	data, err := buildMessageData(t.key, payload)
	if err != nil {
		return err
	}
	session.enqueueMessage(t.key, eventID, data)
	return nil
}

func (t *multiplexTopic) fetchPayload(ctx context.Context) ([]byte, error) {
	response, err := t.fetcher(ctx, t.identifier)
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func buildMessageData(topic string, payload []byte) ([]byte, error) {
	return json.Marshal(messageEnvelope{
		Topic:   topic,
		Payload: json.RawMessage(payload),
	})
}

func parseInitialTopics(query map[string][]string) []string {
	rawTopics := make([]string, 0)
	for _, topic := range query["topic"] {
		if strings.TrimSpace(topic) != "" {
			rawTopics = append(rawTopics, topic)
		}
	}
	if len(rawTopics) > 0 {
		return rawTopics
	}

	for _, group := range query["topics"] {
		for topic := range strings.SplitSeq(group, ",") {
			if strings.TrimSpace(topic) != "" {
				rawTopics = append(rawTopics, topic)
			}
		}
	}
	return rawTopics
}
