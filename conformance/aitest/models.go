// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aitest

import (
	"net/http"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// ModelsJSON is the part of the answer of the models routes that the tests read.
type ModelsJSON struct {
	Supported   bool     `json:"supported"`
	Count       int      `json:"count"`
	Models      []string `json:"models"`
	RefreshedAt string   `json:"refreshedAt"`
	Error       string   `json:"error"`
	ErrorAt     string   `json:"errorAt"`
}

// Models reads the models list that the server keeps for an endpoint.
func (s *Server) Models(name string) ModelsJSON {
	s.t.Helper()
	status, body := s.Call(http.MethodGet, "/ai/endpoints/"+name+"/models", nil)
	require.Equal(s.t, http.StatusOK, status, body)
	var m ModelsJSON
	Decode(s.t, body, &m)
	return m
}

// WaitModels waits until the list of the endpoint has a good read or an error: the
// server makes the first read after it saves the endpoint, and its answer does not wait.
func (s *Server) WaitModels(name string) ModelsJSON {
	s.t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(s.t))
	for time.Now().Before(deadline) {
		if m := s.Models(name); m.RefreshedAt != "" || m.Error != "" {
			return m
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.FailNow(s.t, "the first read of the models list has no result", "%s", s.proc.Output())
	return ModelsJSON{}
}

// Reload asks the server to read the models list now.
func (s *Server) Reload(name string) ModelsJSON {
	s.t.Helper()
	status, body := s.Call(http.MethodPost, "/ai/endpoints/"+name+"/models/reload", nil)
	require.Equal(s.t, http.StatusOK, status, body)
	var m ModelsJSON
	Decode(s.t, body, &m)
	return m
}
