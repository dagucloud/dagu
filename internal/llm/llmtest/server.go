// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package llmtest provides test doubles for LLM provider APIs.
package llmtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Server answers each request with the next scripted response body and
// records the decoded request bodies.
type Server struct {
	// Path is the URL path every request must use.
	Path string
	// Headers are request headers every request must carry.
	Headers map[string]string
	// Responses are the bodies returned in order.
	Responses []string

	mu       sync.Mutex
	requests []map[string]any
}

// Start serves until the test ends and returns the server URL.
func (s *Server) Start(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, s.Path, r.URL.Path)
		for name, value := range s.Headers {
			assert.Equal(t, value, r.Header.Get(name), name)
		}
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		var request map[string]any
		assert.NoError(t, json.Unmarshal(body, &request))

		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, request)
		if len(s.Responses) == 0 {
			http.Error(w, "no scripted response left", http.StatusInternalServerError)
			return
		}
		response := s.Responses[0]
		s.Responses = s.Responses[1:]
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// Requests returns the decoded request bodies received so far.
func (s *Server) Requests() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}
