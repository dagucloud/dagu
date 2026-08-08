// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeWebhookForwardHeader(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "x-github-event", NormalizeWebhookForwardHeader(" X-GitHub-Event "))
	assert.Equal(t, "", NormalizeWebhookForwardHeader("   "))
}

func TestIsDeniedWebhookForwardHeader(t *testing.T) {
	t.Parallel()

	assert.True(t, IsDeniedWebhookForwardHeader("Authorization"))
	assert.True(t, IsDeniedWebhookForwardHeader(" authorization "))
	assert.True(t, IsDeniedWebhookForwardHeader("X-Dagu-Profile"))
	assert.False(t, IsDeniedWebhookForwardHeader("X-GitHub-Event"))
}

func TestIsValidWebhookHeaderToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{name: "simple token", header: "x-github-event", want: true},
		{name: "allowed punctuation", header: "x.custom_header+value", want: true},
		{name: "empty", header: "", want: false},
		{name: "contains whitespace", header: "my header", want: false},
		{name: "contains colon", header: "x:header", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsValidWebhookHeaderToken(tt.header))
		})
	}
}
