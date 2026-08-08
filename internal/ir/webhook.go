// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import "strings"

const (
	webhookAuthorizationHeader = "authorization"
	webhookProfileHeader       = "x-dagu-profile"
)

// NormalizeWebhookForwardHeader canonicalizes a webhook header name for
// config validation and runtime matching.
func NormalizeWebhookForwardHeader(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// IsValidWebhookHeaderToken reports whether name matches the RFC 9110 token
// grammar used by HTTP header field names.
func IsValidWebhookHeaderToken(name string) bool {
	if name == "" {
		return false
	}

	for i := 0; i < len(name); i++ {
		c := name[i]
		if ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}

	return true
}

// IsDeniedWebhookForwardHeader reports whether a webhook header must never be
// forwarded into DAG runtime params.
func IsDeniedWebhookForwardHeader(name string) bool {
	switch NormalizeWebhookForwardHeader(name) {
	case webhookAuthorizationHeader, webhookProfileHeader:
		return true
	default:
		return false
	}
}
