// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package connection holds the domain model of connections: authorized
// accounts at an OAuth provider that DAG steps, mail, and notifications use
// without handling a credential themselves.
//
// A connection is kept as records that different writers own:
//
//	App         an OAuth client registration that connections share,
//	            written by admins
//	Connection  one account's metadata, written by managers and admins
//	TokenState  its tokens, status, and connect session, written only by
//	            the connect flow and the token broker, and only with a
//	            compare-and-swap
//	Catalog     the tools one MCP service of a connection lists
//
// A connection is identified by its [Key]: its workspace and its name.
package connection
