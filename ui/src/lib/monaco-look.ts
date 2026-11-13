// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Options that every Monaco editor of the app shares, so that a small field
 * and the full editor look alike. The module does not import Monaco: the
 * option builders and their tests can use it.
 *
 * @module lib/monaco-look
 */

/** The font of every code editor. */
export const EDITOR_FONT_FAMILY =
  "'JetBrains Mono', 'Fira Code', Menlo, Monaco, 'Courier New', monospace";

/**
 * The Unicode marks of Monaco: no box on a no-break space (French text has
 * them before `:` and `?`), a dash or a quote inside a string or a comment.
 * Outside strings and comments, for example in an indent, the marks stay,
 * because such a character breaks the YAML.
 */
export const EDITOR_UNICODE_HIGHLIGHT = {
  includeStrings: false,
  includeComments: false,
} as const;

/**
 * Word wrap for long text. A line wraps at the indent of the line. The stock
 * break characters include `{`, which would split a `${...}` reference from
 * its `$`: it is not one of them here.
 */
export const EDITOR_WRAP = {
  wordWrap: 'on',
  wrappingIndent: 'same',
  wordWrapBreakBeforeCharacters:
    '([\u2018\u201C\u3008\u300A\u300C\u300E\u3010\u3014\uFF08\uFF3B\uFF5B\uFF62\u00A3\u00A5\uFF04\uFFE1\uFFE5+\uFF0B',
} as const;
