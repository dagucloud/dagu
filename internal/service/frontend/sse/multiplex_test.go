// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/remotenode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTopicCanonicalizesQuery(t *testing.T) {
	parsed, err := ParseTopic("dagslist:perPage=100&page=1")
	require.NoError(t, err)

	assert.Equal(t, TopicTypeDAGsList, parsed.Type)
	assert.Equal(t, "page=1&perPage=100", parsed.Identifier)
	assert.Equal(t, "dagslist:page=1&perPage=100", parsed.Key)
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
	require.NoError(t, topic.sendSnapshot(context.Background(), session, 1))
	assert.Nil(t, session.popNext())
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
	require.NoError(t, topic.sendSnapshot(context.Background(), result.session, 1))
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

	require.NoError(t, topic.sendSnapshot(context.Background(), result.session, 1))
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
