// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Monaco themes matching the app palette: amber keys, green strings, and
 * violet accents on the card surface of each theme.
 *
 * @module lib/monaco-theme
 */
import * as monaco from 'monaco-editor';

export const KILN_DARK = 'kiln-dark';
export const KILN_LIGHT = 'kiln-light';

let registered = false;

/** Registers both themes once per app lifetime. Safe to call repeatedly. */
export function registerKilnThemes() {
  if (registered) return;
  registered = true;

  monaco.editor.defineTheme(KILN_DARK, {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'comment', foreground: '8b8b95' },
      { token: 'string', foreground: '8fd9a8' },
      { token: 'number', foreground: 'd8b4fe' },
      { token: 'keyword', foreground: 'e2a566' },
      { token: 'type', foreground: 'e2a566' },
      { token: 'variable', foreground: 'a89ff8' },
      { token: 'namespace', foreground: 'a89ff8' },
      { token: 'delimiter', foreground: 'c6c8ce' },
      { token: 'operators', foreground: 'c6c8ce' },
    ],
    colors: {
      'editor.background': '#141417',
      'editor.foreground': '#ededf0',
      'editor.lineHighlightBackground': '#1a1a1e',
      'editor.selectionBackground': '#2f2a5a',
      'editorCursor.foreground': '#ededf0',
      'editorLineNumber.foreground': '#86868f',
      'editorLineNumber.activeForeground': '#c4c4cc',
      'editorIndentGuide.background1': '#24242a',
      'editorWidget.background': '#1a1a1e',
      'editorWidget.border': '#32323a',
      'editorSuggestWidget.selectedBackground': '#2f2a5a',
    },
  });

  monaco.editor.defineTheme(KILN_LIGHT, {
    base: 'vs',
    inherit: true,
    rules: [
      { token: 'comment', foreground: '6e6e78' },
      { token: 'string', foreground: '14753a' },
      { token: 'number', foreground: '7c3aed' },
      { token: 'keyword', foreground: '9a5d0b' },
      { token: 'type', foreground: '9a5d0b' },
      { token: 'variable', foreground: '5d4fe0' },
      { token: 'namespace', foreground: '5d4fe0' },
      { token: 'delimiter', foreground: '52525c' },
      { token: 'operators', foreground: '52525c' },
    ],
    colors: {
      'editor.background': '#ffffff',
      'editor.foreground': '#111114',
      'editor.lineHighlightBackground': '#f4f4f6',
      'editor.selectionBackground': '#dedafa',
      'editorCursor.foreground': '#111114',
      'editorLineNumber.foreground': '#71717a',
      'editorLineNumber.activeForeground': '#111114',
      'editorIndentGuide.background1': '#e7e7ec',
      'editorWidget.background': '#ffffff',
      'editorWidget.border': '#d3d3db',
      'editorSuggestWidget.selectedBackground': '#ebe8fc',
    },
  });
}
