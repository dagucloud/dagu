// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
)

// secretValues is a SecretSource whose values and revision a test sets.
type secretValues struct {
	values   []string
	revision uint64
	loads    int
}

func (s *secretValues) CurrentValues(context.Context) ([]string, error) {
	s.loads++
	return s.values, nil
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
