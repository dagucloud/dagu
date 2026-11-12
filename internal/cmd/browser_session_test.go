// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSessionCommand runs dagu browser session with args, reading stdin, and
// returns what it printed.
func runSessionCommand(th test.Command, stdin string, args ...string) (string, error) {
	var out bytes.Buffer
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("context", "", "")
	root.AddCommand(cmd.Browser())
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(test.WithConfigFlag(append([]string{"browser", "session"}, args...), th.Config))
	err := root.ExecuteContext(th.Context)
	return out.String(), err
}

// sessionErrorCode reads the error code a failed session command printed.
func sessionErrorCode(t *testing.T, out string) string {
	t.Helper()
	var printed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &printed), out)
	return printed.Error.Code
}

func TestBrowserSessionCommands(t *testing.T) {
	t.Run("ListsNoSessions", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		out, err := runSessionCommand(th, "", "list")
		require.NoError(t, err)
		assert.JSONEq(t, `{"sessions": [], "capabilities": ["trusted_cas", "write_gate"]}`, out)
	})

	// A command on a session that does not exist prints its error as JSON
	// and fails.
	t.Run("ReportsAnUnknownSession", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		for _, args := range [][]string{
			{"do", "ab2cd3ef4g"},
			{"describe", "ab2cd3ef4g"},
			{"export", "ab2cd3ef4g", "--dag", "orders", "--step", "fetch"},
			{"close", "ab2cd3ef4g"},
			{"close", "ab2cd3ef4g", "--keep"},
			{"gate", "--person", "ab2cd3ef4g"},
		} {
			out, err := runSessionCommand(th, `{"goto": "https://example.com"}`, args...)
			require.Error(t, err, args)
			assert.Equal(t, "session_not_found", sessionErrorCode(t, out), args)
		}
	})

	// Input a session cannot use is refused before any browser starts.
	t.Run("RefusesBadInput", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		for _, tc := range []struct {
			stdin string
			args  []string
		}{
			{args: []string{"open", "--viewport", "wide"}},
			{args: []string{"open", "--idle-timeout", "forever"}},
			{args: []string{"open", "--llm", "{provider: openai, model: gpt-5-mini}", "--model", "gpt-5"}},
			{args: []string{"open", "--llm", "{provider: someai, model: m}"}},
			{args: []string{"open", "--provider", "openai"}},
			{args: []string{"open", "https://evil.example.org", "--allowed-domain", "portal.example.com"}},
			{args: []string{"open", "--outline-chars", "-1"}},
			{stdin: "", args: []string{"do", "ab2cd3ef4g"}},
			{stdin: `{"ask": {"prompt": "Code?", "as": "code"}}`, args: []string{"do", "ab2cd3ef4g"}},
			{stdin: strings.Repeat("x", 1<<20+1), args: []string{"do", "ab2cd3ef4g"}},
			{args: []string{"describe", "ab2cd3ef4g", "--format", "xml"}},
			{args: []string{"export", "ab2cd3ef4g", "--dag", "orders", "--step", "fetch-orders"}},
			{args: []string{"export", "ab2cd3ef4g", "--dag", "orders", "--step", "fetch", "--skip", "first"}},
			{args: []string{"export", "ab2cd3ef4g", "--dag", "orders", "--step", "fetch", "--format", "xml"}},
		} {
			out, err := runSessionCommand(th, tc.stdin, tc.args...)
			require.Error(t, err, tc.args)
			assert.Equal(t, "invalid_input", sessionErrorCode(t, out), tc.args)
		}
	})
}
