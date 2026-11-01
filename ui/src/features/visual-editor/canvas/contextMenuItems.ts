// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  CanvasDirection,
  CanvasEdge,
  CanvasNode,
  StepCommandGates,
} from '../contracts';
import { AGENT_NODE_KEY } from './types';
import { canvasShortcut } from './useCanvasKeymap';

export type ContextMenuKind = 'node' | 'edge' | 'pane';

export type ContextMenuRowId =
  | 'viewLogs'
  | 'openSubRun'
  | 'retryStep'
  | 'setStatus'
  | 'addStep'
  | 'paste'
  | 'insertStep'
  | 'removeDependency'
  | 'fitView'
  | 'directionLR'
  | 'directionTB'
  | 'exportSvg'
  | 'exportPng'
  | 'keyHelp';

/** Rows with the same section stand together; a line separates sections. */
export type ContextMenuSection = 'open' | 'run' | 'edit' | 'view';

export interface ContextMenuRow {
  id: ContextMenuRowId;
  /** English i18n source text. */
  label: string;
  /** Key name, or '' when no key is bound. */
  shortcut: string;
  disabled: boolean;
  /** English i18n source text that tells why the row is disabled. */
  reason?: string;
  /** Set on the direction rows: true for the current direction. */
  checked?: boolean;
  section: ContextMenuSection;
}

/** What an edge menu needs to know about its edge. */
export interface ContextMenuEdge {
  edge: CanvasEdge;
  /** A step can go on the edge. */
  insertable: boolean;
  /** Null when the edge can be removed. */
  lockReason: string | null;
}

export interface ContextMenuTargets {
  node: CanvasNode;
  edge: ContextMenuEdge;
  pane: null;
}

export interface ContextMenuGates {
  /** False when the canvas is frozen or read-only. */
  editable: boolean;
  /** Absent when the host takes no run commands: the run rows are hidden. */
  stepCommandGates?: StepCommandGates;
  /** Absent reads as nothing to paste. */
  clipboard?: { canPaste: boolean };
  direction: CanvasDirection;
  /** False when the host cannot change the direction. */
  canChangeDirection: boolean;
}

const NOT_AVAILABLE = 'Not available';
const NOTHING_TO_PASTE = 'Nothing to paste';

function row(
  id: ContextMenuRowId,
  label: string,
  section: ContextMenuSection,
  extra: Partial<Pick<ContextMenuRow, 'shortcut' | 'reason' | 'checked'>> = {}
): ContextMenuRow {
  return {
    id,
    label,
    section,
    shortcut: extra.shortcut ?? '',
    disabled: extra.reason !== undefined,
    ...(extra.reason !== undefined ? { reason: extra.reason } : {}),
    ...(extra.checked !== undefined ? { checked: extra.checked } : {}),
  };
}

function pasteRow(gates: ContextMenuGates): ContextMenuRow {
  let reason: string | undefined;
  if (!gates.editable) {
    reason = NOT_AVAILABLE;
  } else if (!(gates.clipboard?.canPaste ?? false)) {
    reason = NOTHING_TO_PASTE;
  }
  return row('paste', 'Paste', 'edit', {
    shortcut: canvasShortcut('paste'),
    reason,
  });
}

function nodeRows(node: CanvasNode, gates: ContextMenuGates): ContextMenuRow[] {
  const isAgent = node.key === AGENT_NODE_KEY || node.kind === 'agent';
  const isHandler = node.lane === 'handler';
  const run = gates.stepCommandGates;
  const name = node.serverName;
  // Open, Run step, Duplicate, Copy and Delete are on the hover toolbar of
  // the node and have their keys (Enter, Ctrl/Cmd+D, Ctrl/Cmd+C, Delete), and
  // F2 renames: the menu has no row for them.
  const rows: ContextMenuRow[] = [];
  if (run && name !== undefined) {
    if (node.run) {
      rows.push(row('viewLogs', 'View logs', 'open'));
    }
    if (run.hasSubRuns(name)) {
      rows.push(row('openSubRun', 'Open sub-run', 'open'));
    }
    if (!isAgent && !isHandler) {
      if (run.canRetry(name)) {
        rows.push(row('retryStep', 'Retry step', 'run'));
      }
      if (run.canSetStatus(name)) {
        rows.push(row('setStatus', 'Change status', 'run'));
      }
    }
  }
  if (isAgent) {
    return rows;
  }
  if (gates.editable && !isHandler) {
    rows.push(
      row('addStep', 'Add step', 'edit', {
        shortcut: canvasShortcut('palette'),
      })
    );
  }
  return rows;
}

function edgeRows(
  target: ContextMenuEdge,
  gates: ContextMenuGates
): ContextMenuRow[] {
  if (!gates.editable) {
    return [];
  }
  const rows: ContextMenuRow[] = [];
  if (target.insertable) {
    rows.push(row('insertStep', 'Insert step', 'edit'));
  }
  if (target.edge.kind === 'depends' || target.edge.kind === 'route') {
    rows.push(
      row('removeDependency', 'Remove dependency', 'edit', {
        shortcut: canvasShortcut('delete'),
        ...(target.lockReason !== null ? { reason: target.lockReason } : {}),
      })
    );
  }
  return rows;
}

function paneRows(gates: ContextMenuGates): ContextMenuRow[] {
  const rows: ContextMenuRow[] = [];
  if (gates.editable) {
    rows.push(
      row('addStep', 'Add step', 'edit', {
        shortcut: canvasShortcut('palette'),
      })
    );
    rows.push(pasteRow(gates));
  }
  rows.push(
    row('fitView', 'Fit view', 'view', { shortcut: canvasShortcut('fit') })
  );
  if (gates.canChangeDirection) {
    rows.push(
      row('directionLR', 'Left to right', 'view', {
        checked: gates.direction === 'LR',
      }),
      row('directionTB', 'Top to bottom', 'view', {
        checked: gates.direction === 'TB',
      })
    );
  }
  rows.push(
    row('exportSvg', 'Export as SVG', 'view'),
    row('exportPng', 'Export as PNG', 'view'),
    row('keyHelp', 'Keyboard shortcuts', 'view', {
      shortcut: canvasShortcut('help'),
    })
  );
  return rows;
}

/**
 * The rows of a canvas menu. Each row is shown only when the user may use
 * it. Paste is disabled while there is nothing to paste.
 */
export function contextMenuItems<K extends ContextMenuKind>(
  kind: K,
  target: ContextMenuTargets[K],
  gates: ContextMenuGates
): ContextMenuRow[] {
  switch (kind) {
    case 'node':
      return nodeRows(target as CanvasNode, gates);
    case 'edge':
      return edgeRows(target as ContextMenuEdge, gates);
    default:
      return paneRows(gates);
  }
}
