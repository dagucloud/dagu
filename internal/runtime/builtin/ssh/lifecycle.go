// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
)

type executorLifecycle struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	transport io.Closer
	resource  io.Closer
	closed    bool
}

func (l *executorLifecycle) begin(ctx context.Context) (context.Context, bool) {
	runCtx, cancel := context.WithCancel(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.cancel != nil {
		cancel()
		return runCtx, false
	}
	l.cancel = cancel
	return runCtx, true
}

func (l *executorLifecycle) registerTransport(transport io.Closer) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.transport != nil {
		return false
	}
	l.transport = transport
	return true
}

func (l *executorLifecycle) registerResource(resource io.Closer) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.resource != nil {
		return false
	}
	l.resource = resource
	return true
}

func (l *executorLifecycle) shutdown(force bool) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	cancel := l.cancel
	l.cancel = nil
	transport := l.transport
	l.transport = nil
	resource := l.resource
	l.resource = nil
	l.mu.Unlock()

	if force {
		if cancel != nil {
			cancel()
		}
		var transportErr, resourceErr error
		if transport != nil {
			transportErr = unexpectedCloseError(transport.Close())
		}
		if resource != nil {
			resourceErr = unexpectedCloseError(resource.Close())
		}
		return errors.Join(transportErr, resourceErr)
	}

	var resourceErr, transportErr error
	if resource != nil {
		resourceErr = unexpectedCloseError(resource.Close())
	}
	if transport != nil {
		transportErr = unexpectedCloseError(transport.Close())
	}
	if cancel != nil {
		cancel()
	}
	return errors.Join(resourceErr, transportErr)
}

func unexpectedCloseError(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		unexpected := make([]error, 0, len(joined.Unwrap()))
		for _, child := range joined.Unwrap() {
			if child = unexpectedCloseError(child); child != nil {
				unexpected = append(unexpected, child)
			}
		}
		return errors.Join(unexpected...)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
