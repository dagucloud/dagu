// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor } from 'monaco-editor';

/** The options of the Monaco YAML editor of the DAG. */
export function buildEditorOptions(opts: {
  readOnly: boolean;
  lineNumbers: boolean;
}): editor.IStandaloneEditorConstructionOptions {
  const { readOnly, lineNumbers } = opts;
  return {
    readOnly,
    // automaticLayout: true,
    minimap: { enabled: false },
    scrollBeyondLastLine: false,
    // The sticky line draws over the first line of a step view.
    stickyScroll: { enabled: false },
    // The suggest and hover widgets draw in the body: the column of the node
    // view clips its overflow.
    fixedOverflowWidgets: true,
    // The dimmed number of the last empty line has a contrast below 3:1.
    renderFinalNewline: 'on',
    quickSuggestions: readOnly
      ? false
      : { other: true, comments: false, strings: true },
    suggestOnTriggerCharacters: !readOnly,
    formatOnType: false,
    formatOnPaste: !readOnly,
    renderValidationDecorations: readOnly ? 'off' : 'on',
    lineNumbers: lineNumbers ? 'on' : 'off',
    // No decoration uses the glyph margin: it would stay an empty column.
    glyphMargin: false,
    fontFamily:
      "'JetBrains Mono', 'Fira Code', Menlo, Monaco, 'Courier New', monospace",
    fontSize: 13,
    padding: {
      top: 8,
      bottom: 8,
    },
  };
}
