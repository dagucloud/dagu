// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package extauthz implements the opt-in external authorization hook
// (auth.external).
//
// An access gateway in front of Dagu authenticates the caller and sets a short
// signed identity token (a JWT signed with EdDSA or ES256) in a configured
// request header. Dagu verifies the token with configured public keys and uses
// its "sub" claim as the subject. For each action Dagu then asks a policy
// decision point (PDP) that implements the OpenID AuthZEN Authorization API 1.0:
//
//	POST <base_url>/access/v1/evaluation
//	{"subject":  {"type": "user", "id": "<sub>"},
//	 "action":   {"name": "runs.start"},
//	 "resource": {"type": "workflow", "id": "<workflow name>"}}
//
// A missing or invalid token is rejected with 401. A "false" decision, a PDP
// error, a timeout or a malformed PDP answer is rejected with 403: the hook
// always fails closed. The package stores no users, roles, grants, sessions or
// decisions other than an optional short in-memory decision cache.
//
// The hook only runs with auth.mode "none". It does not replace, extend or
// bypass the builtin authentication, the role-based access control, SSO or the
// audit log, and it does not change their licence checks.
package extauthz
