// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sql

// Export internal functions for testing.
// This file is only compiled during testing (due to _test.go suffix).

// SplitStatements exports splitStatements for testing.
var SplitStatements = splitStatements

// IsSelectQuery exports isSelectQuery for testing.
var IsSelectQuery = isSelectQuery

// CommentSyntax and PostgresComments export the comment rules for testing.
type CommentSyntax = commentSyntax

var PostgresComments = postgresComments
