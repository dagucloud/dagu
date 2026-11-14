// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor } from 'monaco-editor';
import type { components } from '@/api/v1/schema';
import {
  issueMarkerMessage,
  locateIssue,
} from '@/features/visual-editor/doc/issues';
import {
  parseDagDoc,
  resolveStepNames,
} from '@/features/visual-editor/doc/parse';
import { parseValidationMarkers } from './validationMarkers';

type ValidationIssue = components['schemas']['ValidationIssue'];
type DAGDetails = components['schemas']['DAGDetails'];

// monaco.MarkerSeverity values; literals keep monaco out of this chunk.
const SEVERITY = { error: 8, warning: 4, notice: 2 } as const;

/** Owner of the markers of the issues, for the editor and its code actions. */
export const ISSUE_MARKER_OWNER = 'dagu-server';

/**
 * Editor markers for server validation results. Each structured issue goes
 * on the text range that `locateIssue` finds: its line, its field or its
 * step. An issue without a location falls back to a "[line:column]" prefix
 * of its message. Without issues, the markers come from the error strings,
 * as for servers that send no issues.
 */
export function buildIssueMarkers(
  text: string,
  issues: ValidationIssue[],
  errors: string[],
  details?: DAGDetails
): editor.IMarkerData[] {
  if (issues.length === 0) {
    return parseValidationMarkers(errors).markers;
  }
  const model = parseDagDoc(text);
  const names = resolveStepNames(model, details);
  const markers: editor.IMarkerData[] = [];
  for (const issue of issues) {
    const range = locateIssue(model, issue, names);
    if (!range) {
      for (const marker of parseValidationMarkers([issue.message]).markers) {
        markers.push({ ...marker, severity: SEVERITY[issue.severity] });
      }
      continue;
    }
    markers.push({
      startLineNumber: range.startLine,
      startColumn: range.startColumn,
      endLineNumber: range.endLine,
      endColumn: Math.max(range.endColumn, range.startColumn + 1),
      message: issueMarkerMessage(issue.message),
      severity: SEVERITY[issue.severity],
    });
  }
  return markers;
}
