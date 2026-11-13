// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Monaco themes matching the app palette: amber keys, green strings, and
 * violet accents on the card surface of each theme. The marker, selection and
 * accent colours come from the tokens of the app (`destructive`, `warning`,
 * `primary`, `muted-foreground`, `border`) in the same theme.
 *
 * @module lib/monaco-theme
 */
import * as monaco from 'monaco-editor';
import { DAGU_YAML_LANGUAGE } from './monaco-yaml-language';

export const KILN_DARK = 'kiln-dark';
export const KILN_LIGHT = 'kiln-light';

let registered = false;

/**
 * Registers both themes and the YAML grammar once per app lifetime. Safe to
 * call repeatedly. The grammar replaces the stock one: a prompt in a block
 * scalar is one string, not YAML tokens.
 */
export function registerKilnThemes() {
  if (registered) return;
  registered = true;

  monaco.languages.registerTokensProviderFactory('yaml', {
    create: () => DAGU_YAML_LANGUAGE,
  });

  monaco.editor.defineTheme(KILN_DARK, {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'comment', foreground: '9494a0' },
      { token: 'string', foreground: '8fd9a8' },
      { token: 'string.escape', foreground: 'b4e8c6' },
      { token: 'number', foreground: 'd8b4fe' },
      { token: 'keyword', foreground: 'e2a566' },
      { token: 'type', foreground: 'e2a566' },
      { token: 'variable', foreground: 'a89ff8' },
      { token: 'variable.reference', foreground: 'a89ff8' },
      { token: 'namespace', foreground: 'a89ff8' },
      { token: 'delimiter', foreground: 'c6c8ce' },
      { token: 'operators', foreground: 'c6c8ce' },
    ],
    colors: {
      'editor.background': '#141417',
      'editor.foreground': '#ededf0',
      'editor.lineHighlightBackground': '#1a1a1e',
      'editor.lineHighlightBorder': '#00000000',
      'editor.selectionBackground': '#28244f',
      'editor.inactiveSelectionBackground': '#201e3d',
      'editor.selectionHighlightBackground': '#7c6ef433',
      'editor.wordHighlightBackground': '#7c6ef426',
      'editor.findMatchBackground': '#d9a03c4d',
      'editor.findMatchHighlightBackground': '#d9a03c26',
      'editorBracketMatch.background': '#7c6ef426',
      'editorBracketMatch.border': '#7c6ef480',
      'editorCursor.foreground': '#ededf0',
      'editorGutter.background': '#141417',
      'editorLineNumber.foreground': '#8e8e99',
      'editorLineNumber.activeForeground': '#c4c4cc',
      'editorIndentGuide.background1': '#24242a',
      'editorIndentGuide.activeBackground1': '#32323a',
      'editorWhitespace.foreground': '#32323a',
      'editorError.foreground': '#ef5350',
      'editorWarning.foreground': '#d9a03c',
      'editorInfo.foreground': '#a89ff8',
      'editorOverviewRuler.errorForeground': '#ef5350',
      'editorOverviewRuler.warningForeground': '#d9a03c',
      'editorOverviewRuler.infoForeground': '#a89ff8',
      'editorOverviewRuler.border': '#00000000',
      'editorWidget.background': '#1a1a1e',
      'editorWidget.border': '#32323a',
      'editorHoverWidget.background': '#1a1a1e',
      'editorHoverWidget.border': '#32323a',
      'editorSuggestWidget.background': '#1a1a1e',
      'editorSuggestWidget.border': '#32323a',
      'editorSuggestWidget.selectedBackground': '#2f2a5a',
      'scrollbarSlider.background': '#9a9aa533',
      'scrollbarSlider.hoverBackground': '#9a9aa555',
      'scrollbarSlider.activeBackground': '#9a9aa577',
      focusBorder: '#00000000',
    },
  });

  monaco.editor.defineTheme(KILN_LIGHT, {
    base: 'vs',
    inherit: true,
    rules: [
      { token: 'comment', foreground: '6b6b76' },
      { token: 'string', foreground: '14753a' },
      // The Monaco light theme sets `string.yaml` itself, in blue: the same
      // token here makes the Kiln colour win in the YAML editors.
      { token: 'string.yaml', foreground: '14753a' },
      { token: 'string.escape', foreground: '0f5a2d' },
      { token: 'number', foreground: '7c3aed' },
      { token: 'keyword', foreground: '9a5d0b' },
      { token: 'type', foreground: '9a5d0b' },
      { token: 'variable', foreground: '5d4fe0' },
      { token: 'variable.reference', foreground: '5d4fe0' },
      { token: 'namespace', foreground: '5d4fe0' },
      { token: 'delimiter', foreground: '52525c' },
      { token: 'operators', foreground: '52525c' },
    ],
    colors: {
      'editor.background': '#ffffff',
      'editor.foreground': '#111114',
      'editor.lineHighlightBackground': '#f6f6f8',
      'editor.lineHighlightBorder': '#00000000',
      'editor.selectionBackground': '#ebe8fc',
      'editor.inactiveSelectionBackground': '#f1effc',
      'editor.selectionHighlightBackground': '#5d4fe01f',
      'editor.wordHighlightBackground': '#5d4fe01a',
      'editor.findMatchBackground': '#9a5d0b40',
      'editor.findMatchHighlightBackground': '#9a5d0b1f',
      'editorBracketMatch.background': '#5d4fe01a',
      'editorBracketMatch.border': '#5d4fe066',
      'editorCursor.foreground': '#111114',
      'editorGutter.background': '#ffffff',
      'editorLineNumber.foreground': '#6b6b76',
      'editorLineNumber.activeForeground': '#111114',
      'editorIndentGuide.background1': '#e7e7ec',
      'editorIndentGuide.activeBackground1': '#d3d3db',
      'editorWhitespace.foreground': '#d3d3db',
      'editorError.foreground': '#c8382f',
      'editorWarning.foreground': '#9a5d0b',
      'editorInfo.foreground': '#5d4fe0',
      'editorOverviewRuler.errorForeground': '#c8382f',
      'editorOverviewRuler.warningForeground': '#9a5d0b',
      'editorOverviewRuler.infoForeground': '#5d4fe0',
      'editorOverviewRuler.border': '#00000000',
      'editorWidget.background': '#ffffff',
      'editorWidget.border': '#d3d3db',
      'editorHoverWidget.background': '#ffffff',
      'editorHoverWidget.border': '#d3d3db',
      'editorSuggestWidget.background': '#ffffff',
      'editorSuggestWidget.border': '#d3d3db',
      'editorSuggestWidget.selectedBackground': '#ebe8fc',
      'scrollbarSlider.background': '#6b6b7626',
      'scrollbarSlider.hoverBackground': '#6b6b7640',
      'scrollbarSlider.activeBackground': '#6b6b7659',
      focusBorder: '#00000000',
      // The base theme keeps white text on the selected row, for a dark row.
      'editorSuggestWidget.selectedForeground': '#111114',
      'editorSuggestWidget.selectedIconForeground': '#5d4fe0',
      'editorSuggestWidget.focusHighlightForeground': '#5d4fe0',
    },
  });
}
