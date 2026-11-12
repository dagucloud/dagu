// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Text colours of the code tokens, equal to the token rules of the Monaco
 * themes in `monaco-theme.ts`, so a snippet outside the editor reads like the
 * editor. Each value is a light colour and a dark colour.
 *
 * @module lib/syntax-colors
 */
export type SyntaxTokenKind =
  | 'key'
  | 'string'
  | 'number'
  | 'keyword'
  | 'comment'
  | 'variable'
  | 'punct'
  | 'plain';

export const SYNTAX_CLASS: Readonly<Record<SyntaxTokenKind, string>> = {
  key: 'text-[#9a5d0b] dark:text-[#e2a566]',
  string: 'text-[#14753a] dark:text-[#8fd9a8]',
  number: 'text-[#7c3aed] dark:text-[#d8b4fe]',
  keyword: 'text-[#9a5d0b] dark:text-[#e2a566]',
  comment: 'italic text-[#6e6e78] dark:text-[#8b8b95]',
  variable: 'text-[#5d4fe0] dark:text-[#a89ff8]',
  punct: 'text-[#52525c] dark:text-[#c6c8ce]',
  plain: 'text-foreground',
};
