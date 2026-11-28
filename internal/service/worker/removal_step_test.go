// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// blockingLog holds every log write that contains block until release is
// closed, and closes blocked at the first of them.
type blockingLog struct {
	block    []byte
	blocked  chan struct{}
	release  chan struct{}
	once     sync.Once
	released sync.Once
}

func (l *blockingLog) unblock() { l.released.Do(func() { close(l.release) }) }

func (l *blockingLog) Write(p []byte) (int, error) {
	if bytes.Contains(p, l.block) {
		l.once.Do(func() { close(l.blocked) })
		<-l.release
	}
	return len(p), nil
}

// A real shell step that moved into execution as the refusal arrived does
// not start, also while the removal's log line waits: the removal stops the
// run before it logs (Spec 108 §Removed Worker).
func TestWorkerRemovedRealStep(t *testing.T) {
	th := test.Setup(t)
	for name, removed := range map[string]bool{"Runs": false, "RemovedAsItStarts": true} {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "marker")
			task := &coordinatorv1.Task{
				Operation:      coordinatorv1.Operation_OPERATION_START,
				Target:         "marker",
				Definition:     fmt.Sprintf("name: marker\nsteps:\n  - name: touch\n    run: touch %s\n", marker),
				DagRunId:       "run-" + name,
				RootDagRunName: "marker",
				RootDagRunId:   "run-" + name,
				AttemptKey:     "attempt-" + name,
			}
			handler := &remoteTaskHandler{
				workerID:          "test-worker",
				coordinatorClient: newMockRemoteCoordinatorClient(),
				dagRepository:     th.DAGRepository,
				dagRunMgr:         th.DAGRunMgr,
				serviceRegistry:   th.ServiceRegistry,
				peerConfig:        config.Peer{Insecure: true},
				config:            th.Config,
			}
			// The worker logs to log, which holds the removal's log line
			// until the run ended; the run logs elsewhere.
			log := &blockingLog{block: []byte("Stopping run"), blocked: make(chan struct{}), release: make(chan struct{})}
			t.Cleanup(log.unblock)
			ctx := logger.WithLogger(th.Context, logger.NewLogger(logger.WithWriter(log), logger.WithQuiet()))
			ran := make(chan struct{})
			r := new(refusal)
			w := newRemovableWorker(&removalClient{task: task}, 1, &config.Config{}, r, func(ctx context.Context, task *coordinatorv1.Task) error {
				defer close(ran)
				defer log.unblock()
				return handler.Handle(logger.WithLogger(ctx, logger.NewLogger(logger.WithQuiet())), task)
			})
			if removed {
				w.afterAdmit = func(context.Context) {
					// The refusal arrives from a call of another
					// goroutine, and the run goes on once the removal
					// waits on its log line.
					go r.refuse()
					<-log.blocked
				}
			}

			done := make(chan error, 1)
			go func() { done <- w.Start(ctx) }()
			select {
			case <-ran:
			case <-time.After(30 * time.Second):
				t.Fatal("the run did not end")
			}
			if !removed {
				require.FileExists(t, marker, "the step ran without a removal")
				require.NoError(t, w.Stop(t.Context()))
				return
			}
			select {
			case err := <-done:
				assert.Equal(t, errRemovedForTest, err)
			case <-time.After(10 * time.Second):
				t.Fatal("the removed worker did not stop")
			}
			assert.NoFileExists(t, marker, "the step started after the removal")
		})
	}
}

// The removal ends a claim acknowledgment under way the moment it is
// published, apart from the worker's own stop (Spec 108 §Removal).
func TestRemovalEndsHeldAcknowledgment(t *testing.T) {
	w := NewWorker("test-worker", 1, &removalClient{}, map[string]string{}, &config.Config{}, nil)
	pollCtx, cancelPoll := context.WithCancel(t.Context())
	defer cancelPoll()
	w.pollersMu.Lock()
	w.cancelPoll = cancelPoll
	w.pollersMu.Unlock()

	held := make(chan context.Context, 1)
	task := ownedTask()
	task.ClaimToken = "claim-1"
	client := &removalClient{ackHeld: func(ctx context.Context) {
		held <- ctx
		<-ctx.Done()
	}}
	p := NewPoller("test-worker", client, nil, 0, nil)
	p.removal = w.removal
	acked := make(chan error, 1)
	go func() { acked <- p.ackTaskClaim(pollCtx, task) }()
	ackCtx := <-held

	w.publishRemoval()
	assert.Error(t, ackCtx.Err(), "the acknowledgment went on after the removal was published")
	assert.Error(t, <-acked)
	assert.Equal(t, int32(1), client.acks.Load())
}
