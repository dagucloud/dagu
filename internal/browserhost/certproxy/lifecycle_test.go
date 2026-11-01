// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const testPollInterval = 20 * time.Millisecond

// watch runs watchBrowser and returns a channel closed when it returns.
func watch(t *testing.T, cdpURL string, grace time.Duration) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchBrowser(ctx, cdpURL, grace, testPollInterval)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return done
}

func assertRunning(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("the watch ended while the browser was running")
	case <-time.After(10 * testPollInterval):
	}
}

func assertEnds(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not end")
	}
}

// closedURL is a loopback URL whose connections always fail. Nothing can
// listen on port 0, so a parallel test cannot take it over the way it can
// reuse a freed port.
const closedURL = "http://127.0.0.1:0"

func TestWatchBrowser(t *testing.T) {
	t.Parallel()

	t.Run("EndsWhenBrowserStops", func(t *testing.T) {
		t.Parallel()
		browser := newFakeBrowser(t)
		done := watch(t, browser.server.URL, time.Minute)
		assertRunning(t, done)
		browser.server.Close()
		assertEnds(t, done)
	})

	t.Run("EndsWhenAnotherBrowserAnswers", func(t *testing.T) {
		t.Parallel()
		browser := newFakeBrowser(t)
		done := watch(t, browser.server.URL, time.Minute)
		assertRunning(t, done)
		browser.id.Store("second")
		assertEnds(t, done)
	})

	// Another server can take the port of a browser that closed.
	t.Run("EndsWhenSomethingElseAnswers", func(t *testing.T) {
		t.Parallel()
		browser := newFakeBrowser(t)
		done := watch(t, browser.server.URL, time.Minute)
		assertRunning(t, done)
		browser.replaced.Store(true)
		assertEnds(t, done)
	})

	t.Run("EndsWhenNoBrowserAnswersInTime", func(t *testing.T) {
		t.Parallel()
		const grace = 300 * time.Millisecond
		began := time.Now()
		done := watch(t, closedURL, grace)
		assertEnds(t, done)
		assert.GreaterOrEqual(t, time.Since(began), grace, "a browser not yet started has the grace period to answer")
	})

	t.Run("EndsWithContext", func(t *testing.T) {
		t.Parallel()
		browser := newFakeBrowser(t)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			watchBrowser(ctx, browser.server.URL, time.Minute, testPollInterval)
		}()
		assertRunning(t, done)
		cancel()
		assertEnds(t, done)
	})
}

// A check that runs out of time is a busy browser, not a gone one.
func TestTimedOut(t *testing.T) {
	t.Parallel()

	assert.True(t, timedOut(context.DeadlineExceeded))
	assert.True(t, timedOut(&net.OpError{Op: "dial", Err: timeoutError{}}))
	assert.False(t, timedOut(errors.New("browser DevTools endpoint returned 404 Not Found")))
}

// timeoutError is a network error that ran out of time.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
