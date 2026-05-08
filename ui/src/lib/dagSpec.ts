// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isMap, isScalar, isSeq, parseDocument } from 'yaml';
import {
  withWorkspaceLabel,
  WORKSPACE_LABEL_KEY,
  workspaceLabel,
} from './workspace';

function labelsFromNode(node: unknown): string[] {
  if (isSeq(node)) {
    return node.items
      .map((item) => (isScalar(item) ? String(item.value ?? '') : ''))
      .filter(Boolean);
  }

  if (isScalar(node) && node.value != null) {
    return String(node.value)
      .split(/[,\s]+/)
      .map((label) => label.trim())
      .filter(Boolean);
  }

  return [];
}

export function ensureWorkspaceLabelInDAGSpec(
  spec: string,
  workspaceName: string
): string {
  const label = workspaceLabel(workspaceName);
  if (!label) {
    return spec;
  }

  try {
    const doc = parseDocument(spec);
    if (doc.errors.length > 0 || !isMap(doc.contents)) {
      return spec;
    }

    const labelsNode = doc.contents.get('labels', true);
    if (isMap(labelsNode)) {
      labelsNode.set(WORKSPACE_LABEL_KEY, workspaceName);
    } else {
      const labels = labelsFromNode(labelsNode);
      doc.set('labels', withWorkspaceLabel(labels, workspaceName));
    }
    return doc.toString();
  } catch {
    return spec;
  }
}

export function defaultDAGSpec(workspaceName: string): string {
  return ensureWorkspaceLabelInDAGSpec(
    `steps:\n  - name: hello\n    run: echo hello\n`,
    workspaceName
  );
}
