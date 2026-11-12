// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/remotenode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/singleflight"
)

func TestParseTopicCanonicalizesQuery(t *testing.T) {
	parsed, err := ParseTopic("dagslist:perPage=100&page=1")
	require.NoError(t, err)

	assert.Equal(t, TopicTypeDAGsList, parsed.Type)
	assert.Equal(t, "page=1&perPage=100", parsed.Identifier)
	assert.Equal(t, "dagslist:page=1&perPage=100", parsed.Key)
}

// Browsers that share a workspace and window share one activity topic, so
// per-connection parameters are dropped from its key.
func TestParseTopicSharesActivityAcrossConnections(t *testing.T) {
	parsed, err := ParseTopic("dagrunactivity:workspace=ops&hours=24&token=secret&remoteNode=edge")
	require.NoError(t, err)

	assert.Equal(t, TopicTypeDAGRunActivity, parsed.Type)
	assert.Equal(t, "dagrunactivity:hours=24&workspace=ops", parsed.Key)
}

func TestParseTopicRejectsMalformedQuery(t *testing.T) {
	_, err := ParseTopic("dagruns:%ZZ")
	require.Error(t, err)
}

func TestParseTopicAcceptsSubDAGRunIdentifier(t *testing.T) {
	parsed, err := ParseTopic("subdagrun:billing/run-1/sub-1")
	require.NoError(t, err)

	assert.Equal(t, TopicTypeSubDAGRun, parsed.Type)
	assert.Equal(t, "billing/run-1/sub-1", parsed.Identifier)
	assert.Equal(t, "subdagrun:billing/run-1/sub-1", parsed.Key)
}

func TestParseTopicRejectsDAGTraversalIdentifiers(t *testing.T) {
	tests := []string{
		"dag:../../tmp/secret.yaml",
		"daghistory:..%2F..%2Ftmp%2Fsecret.yaml",
		"dag:foo/bar",
	}

	for _, topic := range tests {
		t.Run(topic, func(t *testing.T) {
			_, err := ParseTopic(topic)
			require.Error(t, err)
		})
	}
}

func TestParseInitialTopics(t *testing.T) {
	query := map[string][]string{
		"topic":  {"dag:test.yaml", "queueitems:default"},
		"topics": {"ignored:topic"},
	}

	assert.Equal(t, []string{"dag:test.yaml", "queueitems:default"}, parseInitialTopics(query))

	query = map[string][]string{
		"topics": {"dag:test.yaml,queueitems:default"},
	}
	assert.Equal(t, []string{"dag:test.yaml", "queueitems:default"}, parseInitialTopics(query))
}

func TestMultiplexerCreateSessionFiltersUnauthorizedTopics(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})
	mux.RegisterFetcher(TopicTypeQueueItems, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})
	mux.RegisterAuthorizer(TopicTypeQueueItems, func(_ context.Context, _ string) error {
		return errors.New("forbidden")
	})

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(
		context.Background(),
		recorder,
		[]string{"dag:test.yaml", "queueitems:default"},
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result.session)

	assert.Equal(t, []string{"dag:test.yaml"}, result.control.Subscribed)
	require.Len(t, result.control.Errors, 1)
	assert.Equal(t, "queueitems:default", result.control.Errors[0].Topic)
}

func TestStreamSessionKeepsWakeNewerThanConcurrentDAGRunBootstrap(t *testing.T) {
	const timeout = 5 * time.Second

	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	started := make(chan struct{})
	bootstrapFetchesRegistered := make(chan struct{})
	releaseBootstrap := make(chan struct{})
	var dayLoads atomic.Int32
	var registeredFetches atomic.Int32
	var dayLoadGroup singleflight.Group
	mux.RegisterFetcher(TopicTypeDAGRuns, func(ctx context.Context, identifier string) (any, error) {
		batchID, ok := persis.DAGRunListReadBatchID(ctx)
		if !ok {
			return nil, errors.New("missing DAG-run list read batch")
		}
		load := dayLoadGroup.DoChan(strconv.FormatUint(batchID, 10), func() (any, error) {
			revision := dayLoads.Add(1)
			if revision == 1 {
				close(started)
				select {
				case <-releaseBootstrap:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return revision, nil
		})
		if registeredFetches.Add(1) == 2 {
			close(bootstrapFetchesRegistered)
		}
		result := <-load
		if result.Err != nil {
			return nil, result.Err
		}
		return map[string]any{"id": identifier, "revision": result.Val}, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)

	result, err := mux.createSession(
		context.Background(),
		httptest.NewRecorder(),
		[]string{"dagruns:status=1", "dagruns:status=4"},
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	done := make(chan struct{})
	go func() {
		result.session.bootstrapTopics(t.Context(), 0, result.topics)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(timeout):
		require.FailNow(t, "DAG-run snapshot bootstrap did not start")
	}
	select {
	case <-bootstrapFetchesRegistered:
	case <-time.After(timeout):
		require.FailNow(t, "DAG-run snapshot fetches did not join the read batch")
	}

	mux.WakeTopic(TopicTypeDAGRuns, "status=4")
	require.Eventually(t, func() bool {
		result.session.mu.Lock()
		defer result.session.mu.Unlock()
		return len(result.session.queue) == 1 && result.session.queue[0].topic == "dagruns:status=4"
	}, timeout, 10*time.Millisecond)

	close(releaseBootstrap)

	select {
	case <-done:
	case <-time.After(timeout):
		require.FailNow(t, "DAG-run snapshot bootstrap did not finish")
	}

	first := result.session.popNext()
	second := result.session.popNext()
	require.NotNil(t, first)
	require.NotNil(t, second)
	assert.Less(t, first.eventID, second.eventID)

	messages := map[string]*queuedMessage{
		first.topic:  first,
		second.topic: second,
	}
	require.Contains(t, messages, "dagruns:status=1")
	require.Contains(t, messages, "dagruns:status=4")
	status4Message := messages["dagruns:status=4"]
	var envelope struct {
		Payload struct {
			Revision int `json:"revision"`
		} `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(status4Message.data, &envelope))
	assert.Equal(t, 2, envelope.Payload.Revision)
	assert.EqualValues(t, 2, dayLoads.Load())
}

func TestStreamSessionIgnoresBootstrapFromPreviousTopicAttachment(t *testing.T) {
	const timeout = 5 * time.Second
	const topicKey = "dagruns:status=4"

	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	started := make(chan struct{})
	releaseBootstrap := make(chan struct{})
	var calls atomic.Int32
	mux.RegisterFetcher(TopicTypeDAGRuns, func(ctx context.Context, _ string) (any, error) {
		revision := calls.Add(1)
		if revision == 1 {
			close(started)
			select {
			case <-releaseBootstrap:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return map[string]any{"revision": revision}, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)

	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), []string{topicKey}, 0)
	require.NoError(t, err)
	defer mux.removeSession(result.session)

	holder, err := mux.createSession(context.Background(), httptest.NewRecorder(), []string{topicKey}, 0)
	require.NoError(t, err)
	defer mux.removeSession(holder.session)

	done := make(chan struct{})
	go func() {
		result.session.bootstrapTopics(t.Context(), 0, result.topics)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(timeout):
		require.FailNow(t, "initial snapshot did not start")
	}

	_, err = mux.mutateSession(t.Context(), result.session.id, nil, []string{topicKey})
	require.NoError(t, err)
	mutation, err := mux.mutateSession(t.Context(), result.session.id, []string{topicKey}, nil)
	require.NoError(t, err)
	require.Len(t, mutation.added, 1)
	result.session.bootstrapTopics(t.Context(), 0, mutation.added)

	close(releaseBootstrap)
	select {
	case <-done:
	case <-time.After(timeout):
		require.FailNow(t, "initial snapshot did not finish")
	}

	message := result.session.popNext()
	require.NotNil(t, message)
	assert.Equal(t, topicKey, message.topic)
	assert.Nil(t, result.session.popNext())

	var envelope struct {
		Payload struct {
			Revision int `json:"revision"`
		} `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(message.data, &envelope))
	assert.Equal(t, 2, envelope.Payload.Revision)
}

func TestMultiplexerCreateSessionFiltersUnsupportedTopics(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAGRunLogs, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(
		context.Background(),
		recorder,
		[]string{"chat:session-1", "dagrunlogs:dag/run-1"},
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result.session)

	assert.Equal(t, []string{"dagrunlogs:dag/run-1"}, result.control.Subscribed)
	require.Len(t, result.control.Errors, 1)
	assert.Equal(t, "chat:session-1", result.control.Errors[0].Topic)
	assert.Equal(t, "unsupported_topic", result.control.Errors[0].Code)
}

func TestMultiplexerMutateSessionPartialAuthorization(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})
	mux.RegisterFetcher(TopicTypeQueueItems, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})
	mux.RegisterAuthorizer(TopicTypeQueueItems, func(_ context.Context, _ string) error {
		return errors.New("forbidden")
	})

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(context.Background(), recorder, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)

	mutation, err := mux.mutateSession(
		context.Background(),
		result.session.id,
		[]string{"dag:test.yaml", "queueitems:default"},
		nil,
	)
	require.NoError(t, err)

	assert.Equal(t, 403, mutation.statusCode)
	assert.Equal(t, []string{"dag:test.yaml"}, mutation.response.Subscribed)
	require.Len(t, mutation.response.Errors, 1)
	assert.Equal(t, "queueitems:default", mutation.response.Errors[0].Topic)
}

func TestMultiplexerMutateSessionPartialUnsupportedTopic(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAGRunLogs, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(context.Background(), recorder, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)

	mutation, err := mux.mutateSession(
		context.Background(),
		result.session.id,
		[]string{"chat:session-1", "dagrunlogs:dag/run-1"},
		nil,
	)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, mutation.statusCode)
	assert.Equal(t, []string{"dagrunlogs:dag/run-1"}, mutation.response.Subscribed)
	require.Len(t, mutation.response.Errors, 1)
	assert.Equal(t, "chat:session-1", mutation.response.Errors[0].Topic)
	assert.Equal(t, "unsupported_topic", mutation.response.Errors[0].Code)
}

func TestMultiplexerCreateSessionFiltersInvalidTopics(t *testing.T) {
	mux := newInvalidDAGRunsTopicMultiplexer(t)

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(
		context.Background(),
		recorder,
		[]string{"dagruns:limit=10", "dagruns:fromDate=abc"},
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result.session)

	assert.Equal(t, []string{"dagruns:limit=10"}, result.control.Subscribed)
	require.Len(t, result.control.Errors, 1)
	assert.Equal(t, "dagruns:fromDate=abc", result.control.Errors[0].Topic)
	assert.Equal(t, "invalid_topic", result.control.Errors[0].Code)
	assert.Equal(t, "invalid fromDate", result.control.Errors[0].Message)
}

func TestMultiplexerMutateSessionPartialInvalidTopic(t *testing.T) {
	mux := newInvalidDAGRunsTopicMultiplexer(t)

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(context.Background(), recorder, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)

	mutation, err := mux.mutateSession(
		context.Background(),
		result.session.id,
		[]string{"dagruns:limit=10", "dagruns:fromDate=abc"},
		nil,
	)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, mutation.statusCode)
	assert.Equal(t, []string{"dagruns:limit=10"}, mutation.response.Subscribed)
	require.Len(t, mutation.response.Errors, 1)
	assert.Equal(t, "dagruns:fromDate=abc", mutation.response.Errors[0].Topic)
	assert.Equal(t, "invalid_topic", mutation.response.Errors[0].Code)
}

// newInvalidDAGRunsTopicMultiplexer serves dagruns topics but rejects the
// identifier "fromDate=abc".
func newInvalidDAGRunsTopicMultiplexer(t *testing.T) *Multiplexer {
	t.Helper()

	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAGRuns, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})
	mux.RegisterValidator(TopicTypeDAGRuns, func(_ context.Context, identifier string) error {
		if identifier == "fromDate=abc" {
			return errors.New("invalid fromDate")
		}
		return nil
	})
	return mux
}

func TestMultiplexerMutateSessionIsAtomicOnInvalidTopicFailure(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(context.Background(), recorder, []string{"dag:test.yaml"}, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)

	_, err = mux.mutateSession(
		context.Background(),
		result.session.id,
		[]string{"invalid-topic"},
		[]string{"dag:test.yaml"},
	)
	require.Error(t, err)

	assert.Equal(t, []string{"dag:test.yaml"}, result.session.topicKeys())
	_, missingTopicExists := mux.topics["invalid-topic"]
	assert.False(t, missingTopicExists)
}

func TestMultiplexerMutateSessionRejectsConflictingTopics(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(context.Background(), recorder, []string{"dag:test.yaml"}, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)

	_, err = mux.mutateSession(
		context.Background(),
		result.session.id,
		[]string{"dag:test.yaml"},
		[]string{"dag:test.yaml"},
	)
	require.ErrorIs(t, err, ErrConflictingTopicMutation)
	assert.Equal(t, []string{"dag:test.yaml"}, result.session.topicKeys())
}

func TestMultiplexerCreateSessionDoesNotRetainTopicsOnFailure(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(
		context.Background(),
		recorder,
		[]string{"dag:test.yaml", "invalid-topic"},
		0,
	)
	require.Error(t, err)
	assert.Nil(t, result.session)
	assert.Empty(t, mux.sessions)
	assert.Empty(t, mux.topics)
}

func TestMultiplexerRetiresUnusedTopicsBeforeReuse(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})

	parsed, err := ParseTopic("dag:test.yaml")
	require.NoError(t, err)

	topic, created, err := mux.getOrCreateTopicForMutation(parsed)
	require.NoError(t, err)
	require.True(t, created)

	session, err := newStreamSession(httptest.NewRecorder(), mux, context.Background())
	require.NoError(t, err)
	require.True(t, session.addTopic(topic))
	require.True(t, topic.addSession(session))

	mux.unsubscribeTopic(session, parsed.Key)

	replacement, replacementCreated, err := mux.getOrCreateTopicForMutation(parsed)
	require.NoError(t, err)
	require.True(t, replacementCreated)
	assert.NotSame(t, topic, replacement)
}

func TestMultiplexTopicSendSnapshotDropsRemovedTopics(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	parsed, err := ParseTopic("dag:test.yaml")
	require.NoError(t, err)

	topic := newMultiplexTopic(mux, parsed, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	}, TopicRefreshModePolling, false)
	session, err := newStreamSession(httptest.NewRecorder(), mux, context.Background())
	require.NoError(t, err)
	require.True(t, session.addTopic(topic))

	require.NotNil(t, session.removeTopic(parsed.Key))
	require.NoError(t, topic.sendSnapshot(context.Background(), session))
	assert.Nil(t, session.popNext())
}

func TestStreamSessionOversizedMessageKeepsSessionOpen(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{WriteBufferSize: 256}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier, "blob": strings.Repeat("x", 1024)}, nil
	})

	result, err := mux.createSession(
		context.Background(),
		httptest.NewRecorder(),
		[]string{"dag:test.yaml"},
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	topic := mux.topics["dag:test.yaml"]
	require.NotNil(t, topic)

	// A payload larger than the write buffer must not close the multiplexed
	// session: the write buffer bounds queued backlog, not a single message.
	require.NoError(t, topic.sendSnapshot(context.Background(), result.session))
	assert.False(t, result.session.isClosed())

	// The session keeps serving updates for the topic afterwards.
	require.NoError(t, topic.sendSnapshot(context.Background(), result.session))
	assert.False(t, result.session.isClosed())

	msg := result.session.popNext()
	require.NotNil(t, msg)
	assert.Equal(t, "dag:test.yaml", msg.topic)
	assert.Nil(t, result.session.popNext())
}

func TestStreamRecoversEvictedSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name   string
		topics []string
	}{
		{"large-first", []string{"dag:large.yaml", "dagrun:small/run-1"}},
		{"large-last", []string{"dagrun:small/run-1", "dag:large.yaml"}},
		{"multiple-large", []string{"dag:large.yaml", "dag:large-2.yaml", "dagrun:small/run-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := NewMultiplexer(StreamConfig{}, nil)
			t.Cleanup(mux.Shutdown)
			fetcher := func(_ context.Context, identifier string) (any, error) {
				blob := ""
				if strings.HasPrefix(identifier, "large") {
					blob = strings.Repeat("x", defaultWriteBufferSize+1024)
				}
				return map[string]string{"id": identifier, "blob": blob}, nil
			}
			for _, topicType := range []TopicType{TopicTypeDAG, TopicTypeDAGRun} {
				mux.RegisterFetcher(topicType, fetcher)
				// Recovery must work without payload changes or external wakes.
				mux.SetRefreshMode(topicType, TopicRefreshModeOnDemand)
			}
			reader, _ := openTestStream(t, mux, tc.topics)
			var delivered []string
			var lastID uint64
			for range tc.topics {
				frame := readTestMessage(t, reader)
				require.Greater(t, frame.id, lastID)
				lastID = frame.id
				var envelope messageEnvelope
				require.NoError(t, json.Unmarshal(frame.data, &envelope))
				delivered = append(delivered, envelope.Topic)
			}
			assert.ElementsMatch(t, tc.topics, delivered)
		})
	}
}

type testStreamFrame struct {
	id    uint64
	event string
	data  []byte
}

func openTestStream(t *testing.T, mux *Multiplexer, topics []string) (*bufio.Reader, *streamSession) {
	t.Helper()
	handler := NewMultiplexHandler(mux, nil)
	server := httptest.NewServer(http.HandlerFunc(handler.HandleStream))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	query := url.Values{"topic": topics}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"?"+query.Encode(), nil)
	require.NoError(t, err)
	response, err := server.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)
	reader := bufio.NewReader(response.Body)
	frame := readTestFrame(t, reader)
	require.Equal(t, "control", frame.event)
	var control StreamControlEvent
	require.NoError(t, json.Unmarshal(frame.data, &control))
	session, err := mux.getSession(control.SessionID)
	require.NoError(t, err)
	return reader, session
}

func readTestMessage(t *testing.T, reader *bufio.Reader) testStreamFrame {
	t.Helper()
	for {
		frame := readTestFrame(t, reader)
		if frame.event == "message" {
			return frame
		}
	}
}

func readTestFrame(t *testing.T, reader *bufio.Reader) testStreamFrame {
	t.Helper()
	var frame testStreamFrame
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			return frame
		case strings.HasPrefix(line, "id: "):
			frame.id, err = strconv.ParseUint(strings.TrimPrefix(line, "id: "), 10, 64)
			require.NoError(t, err)
		case strings.HasPrefix(line, "event: "):
			frame.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			frame.data = []byte(strings.TrimPrefix(line, "data: "))
		}
	}
}

func TestStreamCoalescesOversizedSnapshots(t *testing.T) {
	type userKey struct{}
	fetchCtx := context.WithValue(t.Context(), userKey{}, "alice")
	mux := NewMultiplexer(StreamConfig{WriteBufferSize: 256}, nil)
	t.Cleanup(mux.Shutdown)
	mux.SetRefreshMode(TopicTypeDAG, TopicRefreshModeOnDemand)
	var revision int
	mux.RegisterFetcher(TopicTypeDAG, func(ctx context.Context, id string) (any, error) {
		if ctx.Value(userKey{}) != "alice" {
			return nil, errors.New("missing session user")
		}
		blob := ""
		if id == "a.yaml" && revision > 0 {
			blob = strings.Repeat("x", 1024)
		}
		return map[string]any{"revision": revision, "blob": blob}, nil
	})
	writer := &testFrameWriter{ResponseRecorder: httptest.NewRecorder(), frames: make(chan []byte, 4)}
	result, err := mux.createSession(fetchCtx, writer, []string{"dag:a.yaml", "dag:b.yaml"}, 0)
	require.NoError(t, err)
	t.Cleanup(func() { mux.removeSession(result.session) })
	result.session.bootstrapTopics(fetchCtx, 0, result.topics)
	for revision = 1; revision <= 2; revision++ {
		require.NoError(t, mux.topics["dag:a.yaml"].sendSnapshot(fetchCtx, result.session))
	}
	revision = 2

	// Only one oversized payload is retained; the other topic must still arrive.
	result.session.mu.Lock()
	assert.Len(t, result.session.queue, 1)
	assert.Equal(t, result.session.queue[0].size, result.session.queuedBytes)
	result.session.mu.Unlock()
	startTestSession(t, result.session)
	var delivered []string
	var lastID uint64
	for range 2 {
		frame := readWrittenFrame(t, writer)
		require.Greater(t, frame.id, lastID)
		lastID = frame.id
		var envelope struct {
			Topic   string
			Payload struct{ Revision int }
		}
		require.NoError(t, json.Unmarshal(frame.data, &envelope))
		assert.Equal(t, 2, envelope.Payload.Revision)
		delivered = append(delivered, envelope.Topic)
	}
	assert.Equal(t, []string{"dag:a.yaml", "dag:b.yaml"}, delivered)
}

func TestStreamRetriesRecovery(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{WriteBufferSize: 256}, nil)
	mux.watcherBaseInterval = 20 * time.Millisecond
	t.Cleanup(mux.Shutdown)
	mux.SetRefreshMode(TopicTypeDAG, TopicRefreshModeOnDemand)
	var attempts atomic.Int32
	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, id string) (any, error) {
		if id == "retry.yaml" && attempts.Add(1) == 2 {
			return nil, errors.New("temporary fetch failure")
		}
		blob := ""
		if id == "large.yaml" {
			blob = strings.Repeat("x", 1024)
		}
		return map[string]string{"blob": blob}, nil
	})
	reader, _ := openTestStream(t, mux, []string{"dag:retry.yaml", "dag:other.yaml", "dag:large.yaml"})
	var delivered []string
	for range 3 {
		frame := readTestMessage(t, reader)
		var envelope messageEnvelope
		require.NoError(t, json.Unmarshal(frame.data, &envelope))
		delivered = append(delivered, envelope.Topic)
	}
	assert.Equal(t, []string{"dag:large.yaml", "dag:other.yaml", "dag:retry.yaml"}, delivered)
	assert.EqualValues(t, 3, attempts.Load())
}

func TestStreamRecoveryIgnoresStaleFetch(t *testing.T) {
	for _, reattach := range []bool{false, true} {
		t.Run(strconv.FormatBool(reattach), func(t *testing.T) {
			mux := NewMultiplexer(StreamConfig{WriteBufferSize: 256, HeartbeatInterval: 20 * time.Millisecond}, nil)
			t.Cleanup(mux.Shutdown)
			mux.SetRefreshMode(TopicTypeDAG, TopicRefreshModeOnDemand)
			started := make(chan struct{})
			release := make(chan struct{})
			var attempts atomic.Int32
			mux.RegisterFetcher(TopicTypeDAG, func(ctx context.Context, id string) (any, error) {
				if id == "large.yaml" {
					return strings.Repeat("x", 1024), nil
				}
				if id == "barrier.yaml" {
					return "barrier", nil
				}
				revision := attempts.Add(1)
				if revision == 2 {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return map[string]int32{"revision": revision}, nil
			})
			reader, session := openTestStream(t, mux, []string{"dag:small.yaml", "dag:barrier.yaml", "dag:large.yaml"})
			readTestMessage(t, reader)
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("snapshot recovery did not start")
			}
			// The response writer remains usable while recovery is blocked.
			require.Empty(t, readTestFrame(t, reader).event)
			if reattach {
				_, err := mux.mutateSession(t.Context(), session.id, nil, []string{"dag:small.yaml"})
				require.NoError(t, err)
				mutation, err := mux.mutateSession(t.Context(), session.id, []string{"dag:small.yaml"}, nil)
				require.NoError(t, err)
				session.bootstrapTopics(t.Context(), 0, mutation.added)
			} else {
				mux.WakeTopic(TopicTypeDAG, "small.yaml")
			}
			frame := readTestMessage(t, reader)
			assert.Contains(t, string(frame.data), `"revision":3`)
			close(release)
			// The next recovery can only finish after the stale fetch was rejected.
			frame = readTestMessage(t, reader)
			assert.Contains(t, string(frame.data), `"topic":"dag:barrier.yaml"`)
		})
	}
}

func TestStreamCancelsRecovery(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{WriteBufferSize: 256}, nil)
	t.Cleanup(mux.Shutdown)
	mux.SetRefreshMode(TopicTypeDAG, TopicRefreshModeOnDemand)
	started, canceled := make(chan struct{}), make(chan struct{})
	var attempts atomic.Int32
	mux.RegisterFetcher(TopicTypeDAG, func(ctx context.Context, id string) (any, error) {
		if id == "large.yaml" {
			return strings.Repeat("x", 1024), nil
		}
		if attempts.Add(1) == 2 {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}
		return "small", nil
	})
	_, session := openTestStream(t, mux, []string{"dag:small.yaml", "dag:large.yaml"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("snapshot recovery did not start")
	}
	session.close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("snapshot recovery was not canceled")
	}
}

func TestStreamStopsOnWriteFailure(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{SlowClientTimeout: time.Second}, nil)
	t.Cleanup(mux.Shutdown)
	mux.RegisterFetcher(TopicTypeDAG, func(context.Context, string) (any, error) { return "data", nil })
	mux.SetRefreshMode(TopicTypeDAG, TopicRefreshModeOnDemand)
	writer := &testFrameWriter{ResponseRecorder: httptest.NewRecorder(), writeErr: context.DeadlineExceeded}
	result, err := mux.createSession(t.Context(), writer, []string{"dag:test.yaml"}, 0)
	require.NoError(t, err)
	t.Cleanup(func() { mux.removeSession(result.session) })
	result.session.bootstrapTopics(t.Context(), 0, result.topics)
	require.ErrorIs(t, result.session.Serve(t.Context()), context.DeadlineExceeded)
	assert.False(t, writer.deadline.IsZero())
	assert.True(t, result.session.isClosed())
}

type testFrameWriter struct {
	*httptest.ResponseRecorder
	frames   chan []byte
	writeErr error
	deadline time.Time
}

func (w *testFrameWriter) Write(data []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	w.frames <- append([]byte(nil), data...)
	return len(data), nil
}

func (w *testFrameWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func readWrittenFrame(t *testing.T, writer *testFrameWriter) testStreamFrame {
	t.Helper()
	select {
	case data := <-writer.frames:
		return readTestFrame(t, bufio.NewReader(strings.NewReader(string(data))))
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not deliver a frame")
		return testStreamFrame{}
	}
}

func startTestSession(t *testing.T, session *streamSession) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- session.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
}

func TestMultiplexerSharesTopicRegistryAcrossSessions(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})

	for range 20 {
		recorder := httptest.NewRecorder()
		result, err := mux.createSession(context.Background(), recorder, []string{"dag:test.yaml"}, 0)
		require.NoError(t, err)
		require.NotNil(t, result.session)
	}

	require.Len(t, mux.topics, 1)
	assert.Contains(t, mux.topics, "dag:test.yaml")
}

func TestBuildRemoteEventURLStripsSensitiveQueryParams(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/events/stream?topic=dag%3Atest.yaml&remoteNode=remote1&token=secret", nil)

	remoteURL := buildRemoteStreamURL("https://remote.example.com/api/v1", req.URL.Query())

	assert.Equal(t, "https://remote.example.com/api/v1/events/stream?topic=dag%3Atest.yaml", remoteURL)
}

func TestBuildRemoteTopicMutationURLStripsSensitiveQueryParams(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/events/stream/topics?remoteNode=remote1&token=secret", nil)

	remoteURL := buildRemoteTopicMutationURL("https://remote.example.com/api/v1", req.URL.Query())

	assert.Equal(t, "https://remote.example.com/api/v1/events/stream/topics", remoteURL)
}

func TestMultiplexHandlerProxyStreamForwardsLastEventID(t *testing.T) {
	var forwardedLastEventID string
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedLastEventID = r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: control\ndata: {}\n\n")
	}))
	defer remoteServer.Close()

	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	handler := NewMultiplexHandler(mux, remotenode.NewResolver([]config.RemoteNode{
		{
			Name:       "remote1",
			APIBaseURL: remoteServer.URL,
		},
	}, nil))

	req := httptest.NewRequest("GET", "/api/v1/events/stream?remoteNode=remote1&topic=dag%3Atest.yaml", nil)
	req.Header.Set("Last-Event-ID", "47")
	recorder := httptest.NewRecorder()

	handler.proxyStreamToRemoteNode(recorder, req, "remote1")

	assert.Equal(t, "47", forwardedLastEventID)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestMultiplexHandlerHandleStreamAllowsUnsupportedInitialTopics(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{HeartbeatInterval: time.Hour}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAGRunLogs, func(_ context.Context, identifier string) (any, error) {
		return map[string]string{"id": identifier}, nil
	})

	handler := NewMultiplexHandler(mux, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/events/stream?topic=chat%3Asession-1&topic=dagrunlogs%3Adag%2Frun-1",
		nil,
	).WithContext(ctx)
	recorder := httptest.NewRecorder()

	handler.HandleStream(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)

	body := recorder.Body.String()
	assert.NotContains(t, body, "unable to open SSE stream")

	control := parseControlEvent(t, body)
	assert.Equal(t, []string{"dagrunlogs:dag/run-1"}, control.Subscribed)
	require.Len(t, control.Errors, 1)
	assert.Equal(t, "chat:session-1", control.Errors[0].Topic)
	assert.Equal(t, "unsupported_topic", control.Errors[0].Code)
}

func TestMultiplexerWakeTopicTriggersImmediateRefetch(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	var fetches atomic.Int64
	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		return map[string]any{
			"id":    identifier,
			"count": fetches.Add(1),
		}, nil
	})

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(context.Background(), recorder, []string{"dag:test.yaml"}, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	require.Eventually(t, func() bool {
		return fetches.Load() > 0
	}, time.Second, 10*time.Millisecond)

	before := fetches.Load()
	mux.WakeTopic(TopicTypeDAG, "test.yaml")

	require.Eventually(t, func() bool {
		return fetches.Load() > before
	}, time.Second, 10*time.Millisecond)
}

func TestMultiplexerOnDemandTopicOnlyRefetchesOnWake(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	mux.watcherBaseInterval = 20 * time.Millisecond
	mux.watcherMaxInterval = 20 * time.Millisecond
	t.Cleanup(mux.Shutdown)

	var fetches atomic.Int64
	mux.RegisterFetcher(TopicTypeDAGRun, func(_ context.Context, identifier string) (any, error) {
		return map[string]any{
			"id":    identifier,
			"count": fetches.Add(1),
		}, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRun, TopicRefreshModeOnDemand)

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(context.Background(), recorder, []string{"dagrun:test/run-1"}, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	topic := mux.topics["dagrun:test/run-1"]
	require.NotNil(t, topic)
	require.NoError(t, topic.sendSnapshot(context.Background(), result.session))
	assert.EqualValues(t, 1, fetches.Load())

	require.Never(t, func() bool {
		return fetches.Load() != 1
	}, 200*time.Millisecond, 20*time.Millisecond, "on-demand topic should not refetch without a wake")

	mux.WakeTopic(TopicTypeDAGRun, "test/run-1")

	require.Eventually(t, func() bool {
		return fetches.Load() == 2
	}, time.Second, 10*time.Millisecond)
}

func TestMultiplexerWakeTopicFetchesWithDetachedSessionContext(t *testing.T) {
	type ctxKey string
	const userKey ctxKey = "user"

	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAGRuns, func(ctx context.Context, identifier string) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		user, _ := ctx.Value(userKey).(string)
		if user == "" {
			return nil, errors.New("missing user in fetch context")
		}
		return map[string]string{
			"id":   identifier,
			"user": user,
		}, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)

	topicKey := "dagruns:workspace=all"
	aliceCtx, cancelAlice := context.WithCancel(context.WithValue(context.Background(), userKey, "alice"))
	aliceResult, err := mux.createSession(aliceCtx, httptest.NewRecorder(), []string{topicKey}, 0)
	require.NoError(t, err)
	require.NotNil(t, aliceResult.session)
	defer mux.removeSession(aliceResult.session)
	cancelAlice()

	bobCtx, cancelBob := context.WithCancel(context.WithValue(context.Background(), userKey, "bob"))
	bobResult, err := mux.createSession(bobCtx, httptest.NewRecorder(), []string{topicKey}, 0)
	require.NoError(t, err)
	require.NotNil(t, bobResult.session)
	defer mux.removeSession(bobResult.session)
	cancelBob()

	mux.WakeTopic(TopicTypeDAGRuns, "workspace=all")

	assertSessionPayloadUser := func(session *streamSession, expected string) bool {
		msg := session.popNext()
		if msg == nil {
			return false
		}
		var envelope struct {
			Payload struct {
				User string `json:"user"`
			} `json:"payload"`
		}
		require.NoError(t, json.Unmarshal(msg.data, &envelope))
		return envelope.Payload.User == expected
	}

	require.Eventually(t, func() bool {
		return assertSessionPayloadUser(aliceResult.session, "alice")
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		return assertSessionPayloadUser(bobResult.session, "bob")
	}, time.Second, 10*time.Millisecond)
}

func TestMultiplexerPublishOnWakeEmitsMessageEvenWhenPayloadHashIsUnchanged(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	mux.RegisterFetcher(TopicTypeDAGRuns, func(_ context.Context, identifier string) (any, error) {
		return map[string]any{
			"id":      identifier,
			"dagRuns": []string{"same"},
		}, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)
	mux.SetPublishOnWake(TopicTypeDAGRuns, true)

	recorder := httptest.NewRecorder()
	result, err := mux.createSession(context.Background(), recorder, []string{"dagruns:fromDate=1&toDate=2"}, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	topic := mux.topics["dagruns:fromDate=1&toDate=2"]
	require.NotNil(t, topic)

	require.NoError(t, topic.sendSnapshot(context.Background(), result.session))
	first := result.session.popNext()
	require.NotNil(t, first)

	mux.WakeTopic(TopicTypeDAGRuns, "fromDate=1&toDate=2")

	require.Eventually(t, func() bool {
		return result.session.popNext() != nil
	}, time.Second, 10*time.Millisecond)
}

func parseControlEvent(t *testing.T, body string) StreamControlEvent {
	t.Helper()

	for frame := range strings.SplitSeq(body, "\n\n") {
		if !strings.Contains(frame, "event: control\n") {
			continue
		}

		for line := range strings.SplitSeq(frame, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			var control StreamControlEvent
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &control))
			return control
		}
	}

	t.Fatalf("control event not found in stream body: %q", body)
	return StreamControlEvent{}
}

// A burst of wakes inside the minimum interval costs one immediate refetch and
// one deferred refetch, not one refetch per wake.
func TestMultiplexTopicMinIntervalMergesWakes(t *testing.T) {
	// Above the 1s adaptive floor, so the minimum interval decides the delay.
	const interval = 1200 * time.Millisecond

	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	var fetches atomic.Int32
	mux.RegisterFetcher(TopicTypeDAGRuns, func(context.Context, string) (any, error) {
		return map[string]int32{"revision": fetches.Add(1)}, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)
	mux.SetMinInterval(TopicTypeDAGRuns, interval)

	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), []string{"dagruns:status=1"}, 0)
	require.NoError(t, err)
	defer mux.removeSession(result.session)
	result.session.bootstrapTopics(t.Context(), 0, result.topics)
	require.Equal(t, int32(1), fetches.Load())

	mux.WakeTopic(TopicTypeDAGRuns, "status=1")
	require.Eventually(t, func() bool { return fetches.Load() == 2 }, time.Second, 5*time.Millisecond)

	for range 5 {
		mux.WakeTopic(TopicTypeDAGRuns, "status=1")
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, int32(2), fetches.Load(), "wakes inside the interval wait")

	require.Eventually(t, func() bool { return fetches.Load() == 3 }, 2*interval, 5*time.Millisecond)
	time.Sleep(interval)
	assert.Equal(t, int32(3), fetches.Load(), "the burst becomes one deferred refetch")
}

// An on-demand topic with a maximum interval refetches without wakes, and a
// wake held back by the minimum interval does not wait for that refetch.
func TestMultiplexTopicMaxIntervalRefetchesWithoutWakes(t *testing.T) {
	// Above the 1s adaptive floor, so the minimum interval decides the delay.
	const minInterval = 1200 * time.Millisecond
	const maxInterval = 3 * time.Second

	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	var fetches atomic.Int32
	mux.RegisterFetcher(TopicTypeDAGRuns, func(context.Context, string) (any, error) {
		return map[string]int32{"revision": fetches.Add(1)}, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)
	mux.SetMinInterval(TopicTypeDAGRuns, minInterval)
	mux.SetMaxInterval(TopicTypeDAGRuns, maxInterval)

	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), []string{"dagruns:status=1"}, 0)
	require.NoError(t, err)
	defer mux.removeSession(result.session)
	result.session.bootstrapTopics(t.Context(), 0, result.topics)
	require.Equal(t, int32(1), fetches.Load())

	mux.WakeTopic(TopicTypeDAGRuns, "status=1")
	require.Eventually(t, func() bool { return fetches.Load() == 2 }, time.Second, 5*time.Millisecond)

	mux.WakeTopic(TopicTypeDAGRuns, "status=1")
	require.Eventually(t, func() bool { return fetches.Load() == 3 }, 2*minInterval, 5*time.Millisecond,
		"the held-back wake refetches at the minimum interval")

	require.Eventually(t, func() bool { return fetches.Load() == 4 }, 2*maxInterval, 5*time.Millisecond,
		"the maximum interval refetches without a wake")
}

// Topic fetches run on a background goroutine, where an unrecovered panic
// would end the server process. A panicking fetch counts as a failed fetch,
// and the topic keeps refreshing.
func TestMultiplexTopicSurvivesFetchPanic(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	var fetches atomic.Int32
	mux.RegisterFetcher(TopicTypeDAG, func(_ context.Context, identifier string) (any, error) {
		if fetches.Add(1) == 1 {
			panic("fetch failed")
		}
		return map[string]string{"id": identifier}, nil
	})

	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), []string{"dag:test.yaml"}, 0)
	require.NoError(t, err)
	defer mux.removeSession(result.session)

	// The failed fetch backs off for about a second before the retry.
	require.Eventually(t, func() bool {
		return result.session.popNext() != nil
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, int32(2), fetches.Load())
}
