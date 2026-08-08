// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor } from 'monaco-editor';

// monaco.MarkerSeverity.Error; a literal keeps monaco out of this chunk.
const MARKER_SEVERITY_ERROR = 8;

/**
 * Splits server validation errors into editor markers and unpositioned
 * messages. Only errors whose first line starts with a "[line:column]" prefix
 * produce markers; the rest are returned for list-style display.
 */
export function parseValidationMarkers(errors: string[]): {
  markers: editor.IMarkerData[];
  unpositioned: string[];
} {
  const markers: editor.IMarkerData[] = [];
  const unpositioned: string[] = [];

  for (const error of errors) {
    const firstLine = error.split('\n', 1)[0] ?? '';
    const match = /^\[(\d+):(\d+)\]\s*(.*)$/.exec(firstLine);
    if (!match) {
      unpositioned.push(error);
      continue;
    }

    const line = Number(match[1]);
    const column = Math.max(1, Number(match[2]));
    markers.push({
      startLineNumber: line,
      startColumn: column,
      endLineNumber: line,
      endColumn: column + 1,
      message: match[3] || firstLine,
      severity: MARKER_SEVERITY_ERROR,
    });
  }

  return { markers, unpositioned };
}
