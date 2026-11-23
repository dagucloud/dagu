// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
)

// secretValues is a SecretSource whose values, revision, and load error a
// test sets.
type secretValues struct {
	values   []string
	revision uint64
	err      error
	loads    int
}

func (s *secretValues) CurrentValues(context.Context) ([]string, error) {
	s.loads++
	return s.values, s.err
}

func (s *secretValues) Revision() uint64 { return s.revision }

// Every field but the control fields is masked, so a secret whose value is
// "denied" hides itself in the text fields and still leaves the result.
func TestMaskHidesSecretsOutsideControlFields(t *testing.T) {
	masker := NewMasker(&secretValues{values: []string{"s3cr3t", "denied"}})
	entry := &Entry{
		ID: "id-1", Timestamp: time.Unix(0, 0).UTC(), Category: CategoryMCP, Action: "mcp.tool_call.denied",
		Source: "mcp", Surface: "mcp", Result: "denied", CorrelationID: "corr-1",
		ResourceType: "dag", ResourceID: "dags/s3cr3t", Workspace: "s3cr3t",
		CredentialID: "key-1", CredentialType: "basic", MCPTool: "read s3cr3t",
		UserID: "s3cr3t", Username: "s3cr3t", IPAddress: "s3cr3t",
		Details: `{"command":"echo s3cr3t","why":"denied"}`,
	}

	masker.Mask(context.Background(), entry)

	assert.True(t, entry.Masked)
	assert.Equal(t, "denied", entry.Result)
	assert.Equal(t, "mcp.tool_call.denied", entry.Action)
	for name, field := range map[string]string{
		"resource_id": entry.ResourceID, "workspace": entry.Workspace, "mcp_tool": entry.MCPTool,
		"user_id": entry.UserID, "username": entry.Username, "ip_address": entry.IPAddress,
		"details": entry.Details,
	} {
		assert.NotContains(t, field, "s3cr3t", name)
		assert.Contains(t, field, masking.DefaultMaskString, name)
	}
	assert.True(t, json.Valid([]byte(entry.Details)), "details stay JSON")
}

// A secret that is a whole JSON value, even an object or a quoted string,
// becomes a masked string, and the values around it keep their structure.
// An object holding a token is masked whole, whatever its key order.
func TestMaskKeepsDetailsJSON(t *testing.T) {
	credential := `{"secret_access_key":"wJalrXUtnFEMI","access_key_id":"AKIA` + strings.Repeat("Z", 16) + `"}`
	masker := NewMasker(&secretValues{values: []string{"1234", `{"token":"abc"}`, `"xyz"`, credential}})
	for details, want := range map[string]string{
		`{"pin":1234,"duration_ms":1234}`:         `{"pin":"*******","duration_ms":"*******"}`,
		`{"response":{"token":"abc"},"other":42}`: `{"response":"*******","other":42}`,
		`{"response":"xyz","other":42}`:           `{"response":"*******","other":42}`,
		`{"request":` + credential + `,"n":1}`:    `{"request":"*******","n":1}`,
	} {
		entry := &Entry{Details: details}

		masker.Mask(context.Background(), entry)

		assert.JSONEq(t, want, entry.Details)
	}
}

// A token and a stored value that overlap are masked as one span, so
// neither leaves part of the other in the entry.
func TestMaskHidesOverlappingTokensAndStoredValues(t *testing.T) {
	token := "ghp_" + strings.Repeat("a", 16) + "pin1" + strings.Repeat("b", 16)
	credential := `{"aws_access_key_id":"AKIA` + strings.Repeat("Z", 16) +
		`","aws_secret_access_key":"wJalrXUtnFEMI/K7MDENG"}`
	for name, tc := range map[string]struct{ stored, command string }{
		"a stored value inside a token":     {stored: "pin1", command: "echo " + token},
		"a token inside a stored value":     {stored: credential, command: "echo " + credential},
		"a stored value past a token's end": {stored: strings.Repeat("b", 16) + "-tail", command: "echo " + token + "-tail"},
	} {
		details, err := json.Marshal(map[string]string{"command": tc.command})
		require.NoError(t, err)
		entry := &Entry{Details: string(details)}

		NewMasker(&secretValues{values: []string{tc.stored}}).Mask(context.Background(), entry)

		assert.Equal(t, `{"command":"echo `+masking.DefaultMaskString+`"}`, entry.Details, name)
	}
}

// An entry masked without the secret values carries no marker, so it is
// never sent, and the next entry loads them again.
func TestMaskMarksNothingWhenSecretsFailToLoad(t *testing.T) {
	source := &secretValues{values: []string{"s3cr3t"}, err: errors.New("store unavailable")}
	masker := NewMasker(source)
	failed := &Entry{Username: "s3cr3t"}

	masker.Mask(context.Background(), failed)

	assert.False(t, failed.Masked)
	source.err = nil
	next := &Entry{Username: "s3cr3t"}
	masker.Mask(context.Background(), next)
	assert.True(t, next.Masked)
	assert.Equal(t, masking.DefaultMaskString, next.Username)
	assert.Equal(t, 2, source.loads)
}

func TestMaskHidesCommonTokenFormats(t *testing.T) {
	tokens := []string{
		"ghp_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("B", 22),
		"AKIA" + strings.Repeat("Z", 16),
		"xoxb-1234567890-abcdef",
	}
	for _, token := range tokens {
		entry := &Entry{Details: fmt.Sprintf(`{"command":"git push https://%s@github.com/o/r"}`, token)}

		NewMasker(nil).Mask(context.Background(), entry)

		assert.NotContains(t, entry.Details, token)
		assert.True(t, entry.Masked)
	}
}

// A secret changed in this process is masked in the next entry; one changed
// elsewhere is masked once the values are a minute old.
func TestMaskReloadsChangedSecrets(t *testing.T) {
	source := &secretValues{values: []string{"first"}}
	now := time.Unix(0, 0)
	masker := NewMasker(source)
	masker.now = func() time.Time { return now }
	mask := func(text string) string {
		entry := &Entry{Details: text}
		masker.Mask(context.Background(), entry)
		return entry.Details
	}

	assert.Equal(t, masking.DefaultMaskString, mask("first"))

	source.values, source.revision = []string{"second"}, 1
	assert.Equal(t, masking.DefaultMaskString, mask("second"), "a change in this process")

	source.values = []string{"third"}
	assert.Equal(t, "third", mask("third"), "a change elsewhere, within the minute")
	now = now.Add(secretsMaxAge)
	assert.Equal(t, masking.DefaultMaskString, mask("third"), "a change elsewhere, a minute on")
	assert.Equal(t, 3, source.loads)
}

// RFC 021 asks for under 1 ms per entry with 5,000 secrets.
func BenchmarkMaskWith5000Secrets(b *testing.B) {
	values := make([]string, 5000)
	for i := range values {
		values[i] = fmt.Sprintf("secret-value-%04d-%s", i, strings.Repeat("x", 24))
	}
	masker := NewMasker(&secretValues{values: values})
	entry := Entry{
		Action: "command", Username: "alice", UserID: "user-1", IPAddress: "10.0.0.1",
		Details: `{"connection_id":"c-1","command":"curl -H 'Authorization: Bearer secret-value-4999-` +
			strings.Repeat("x", 24) + `' https://example.com/api"}`,
	}
	masker.Mask(context.Background(), &Entry{})
	b.ResetTimer()
	for b.Loop() {
		e := entry
		masker.Mask(context.Background(), &e)
	}
}
