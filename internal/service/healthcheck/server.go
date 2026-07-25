// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server represents a lightweight HTTP health check server for a service.
type Server struct {
	mu         sync.Mutex
	server     *http.Server
	listener   net.Listener
	port       int
	listenAddr string
	boundAddr  string
	service    string
}

// Response represents the health check response.
type Response struct {
	Status string `json:"status"`
}

// NewServer creates a new health check server for the given service and port.
func NewServer(service string, port int) *Server {
	return &Server{
		port:       port,
		listenAddr: fmt.Sprintf(":%d", port),
		service:    service,
	}
}

// NewServerWithAddr creates a health check server bound to an explicit address.
// It is primarily intended for tests and diagnostics.
func NewServerWithAddr(service, addr string) *Server {
	return &Server{
		listenAddr: addr,
		service:    service,
	}
}

// URL returns the currently bound HTTP base URL for the health server.
// It is primarily intended for tests and diagnostics.
func (h *Server) URL() string {
	if h == nil {
		return ""
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.boundAddr == "" {
		return ""
	}
	return "http://" + h.boundAddr
}

// Start starts the health check server.
func (h *Server) Start(ctx context.Context) error {
	if h.listenAddr == "" || (h.port == 0 && h.listenAddr == ":0") {
		logger.Info(ctx, "Health check server disabled",
			slog.String("service", h.serviceName()),
			tag.Port(h.port),
		)
		return nil
	}

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Get("/health", h.healthHandler)

	server := &http.Server{
		Addr:              h.listenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := net.Listen("tcp", h.listenAddr)
	if err != nil {
		return err
	}
	boundAddr := listener.Addr().String()

	h.mu.Lock()
	if h.server != nil {
		h.mu.Unlock()
		_ = listener.Close()
		return nil
	}
	h.server = server
	h.listener = listener
	h.boundAddr = boundAddr
	h.mu.Unlock()

	go func(server *http.Server, listener net.Listener) {
		logger.Info(ctx, "Starting health check server",
			slog.String("service", h.serviceName()),
			tag.Port(h.port),
		)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error(ctx, "Health check server error",
				slog.String("service", h.serviceName()),
				tag.Error(err),
			)
		}
	}(server, listener)

	return nil
}

// Stop gracefully stops the health check server.
func (h *Server) Stop(ctx context.Context) error {
	h.mu.Lock()
	server := h.server
	listener := h.listener
	if server == nil {
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()

	logger.Info(ctx, "Stopping health check server", slog.String("service", h.serviceName()))

	parentCtx := ctx
	if parentCtx == nil || parentCtx.Err() != nil {
		parentCtx = context.Background()
	}

	shutdownCtx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()

	err := server.Shutdown(shutdownCtx)
	if listener != nil {
		_ = listener.Close()
	}

	h.mu.Lock()
	if h.server == server {
		h.server = nil
		h.listener = nil
		h.boundAddr = ""
	}
	h.mu.Unlock()

	if err != nil {
		logger.Error(ctx, "Failed to shutdown health check server",
			slog.String("service", h.serviceName()),
			tag.Error(err),
		)
		return err
	}
	return nil
}

func (h *Server) healthHandler(w http.ResponseWriter, _ *http.Request) {
	response := Response{Status: "healthy"}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error(context.Background(), "Failed to encode health response",
			slog.String("service", h.serviceName()),
			tag.Error(err),
		)
	}
}

func (h *Server) serviceName() string {
	if h == nil || h.service == "" {
		return "service"
	}
	return h.service
}
