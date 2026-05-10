// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
)

var ErrServerRequestedShutdown = errors.New(
	"socket frontend is requested to shutdown",
)

const idleTimeout = 30 * time.Second

// Server is a unix socket frontend that passes http requests to HandlerFunc.
type Server struct {
	addr        string
	handlerFunc HTTPHandlerFunc

	listener   net.Listener
	httpServer *http.Server

	quit atomic.Bool
	mu   sync.Mutex
}

// HTTPHandlerFunc is a function that handles HTTP requests.
type HTTPHandlerFunc func(w http.ResponseWriter, r *http.Request)

// NewServer creates a new unix socket frontend.
func NewServer(
	addr string,
	handlerFunc HTTPHandlerFunc,
) (*Server, error) {
	return &Server{
		addr:        addr,
		handlerFunc: handlerFunc,
	}, nil
}

// Serve starts listening and serving requests.
func (srv *Server) Serve(ctx context.Context, listen chan error) error {
	_ = os.Remove(srv.addr)
	listener, err := net.Listen("unix", srv.addr)
	if err != nil {
		err = wrapListenError(err)
		if listen != nil {
			listen <- err
		}
		return err
	}

	httpServer := srv.newHTTPServer(ctx)

	if !srv.install(listener, httpServer) {
		if listen != nil {
			listen <- nil
		}
		_ = listener.Close()
		_ = os.Remove(srv.addr)
		return ErrServerRequestedShutdown
	}

	if listen != nil {
		listen <- nil
	}
	logger.Debug(ctx, "Unix socket is listening", tag.Addr(srv.addr))

	defer func() {
		srv.clear(listener, httpServer)
		_ = os.Remove(srv.addr)
	}()

	err = httpServer.Serve(listener)
	if isClosedServerError(err) && srv.quit.Load() {
		return ErrServerRequestedShutdown
	}
	return err
}

// newHTTPServer builds the HTTP server used for unix socket requests.
func (srv *Server) newHTTPServer(ctx context.Context) *http.Server {
	return &http.Server{
		Handler:           srv.httpHandler(ctx),
		ReadHeaderTimeout: defaultTimeout,
		IdleTimeout:       idleTimeout,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
}

// httpHandler adapts the raw handler and recovers panics into HTTP 500 responses.
func (srv *Server) httpHandler(ctx context.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error(
					ctx,
					"Socket handler panicked",
					slog.Any("panic", recovered),
					slog.String("stack", string(debug.Stack())),
				)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		srv.handlerFunc(w, r)
	})
}

// Shutdown stops the frontend.
func (srv *Server) Shutdown(ctx context.Context) error {
	srv.mu.Lock()
	srv.quit.Store(true)
	httpServer := srv.httpServer
	srv.httpServer = nil
	srv.listener = nil
	srv.mu.Unlock()

	if httpServer != nil {
		if err := httpServer.Shutdown(ctx); err != nil && !isClosedServerError(err) {
			logger.Error(ctx, "Failed to shutdown HTTP server", tag.Error(err))
			return err
		}
		return nil
	}

	return nil
}

// install records the live listener/server pair if shutdown has not started.
func (srv *Server) install(listener net.Listener, httpServer *http.Server) bool {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.quit.Load() {
		return false
	}
	srv.listener = listener
	srv.httpServer = httpServer
	return true
}

// clear drops the listener/server pair that finished serving.
func (srv *Server) clear(listener net.Listener, httpServer *http.Server) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.listener == listener {
		srv.listener = nil
	}
	if srv.httpServer == httpServer {
		srv.httpServer = nil
	}
}

// isClosedServerError reports whether an error is expected during graceful shutdown.
func isClosedServerError(err error) bool {
	return errors.Is(err, http.ErrServerClosed) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, os.ErrClosed)
}
