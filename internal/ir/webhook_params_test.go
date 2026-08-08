// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import "testing"

func TestBuildWebhookRuntimeParams(t *testing.T) {
	t.Parallel()

	t.Run("payload and headers only", func(t *testing.T) {
		t.Parallel()

		got := BuildWebhookRuntimeParams(`{"event":"push"}`, `{"x-github-event":["push"]}`, nil)

		want := `WEBHOOK_PAYLOAD="{\"event\":\"push\"}" WEBHOOK_HEADERS="{\"x-github-event\":[\"push\"]}"`
		if got != want {
			t.Fatalf("BuildWebhookRuntimeParams() = %q, want %q", got, want)
		}
	})

	t.Run("extras are appended in key order and empty values are skipped", func(t *testing.T) {
		t.Parallel()

		got := BuildWebhookRuntimeParams("{}", "{}", map[string]string{
			"GITHUB_REF":        "refs/heads/main",
			"GITHUB_EVENT_NAME": "push",
			"GITHUB_SHA":        "",
		})

		want := `WEBHOOK_PAYLOAD="{}" WEBHOOK_HEADERS="{}" GITHUB_EVENT_NAME="push" GITHUB_REF="refs/heads/main"`
		if got != want {
			t.Fatalf("BuildWebhookRuntimeParams() = %q, want %q", got, want)
		}
	})
}
