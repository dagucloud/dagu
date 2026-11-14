// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus } from '@/api/v1/schema';
import type { CanvasEdge, CanvasNode, StepCommandGates } from '../../contracts';
import {
  contextMenuItems,
  type ContextMenuEdge,
  type ContextMenuGates,
  type ContextMenuRowId,
} from '../contextMenuItems';
import { AGENT_NODE_KEY } from '../types';

const step: CanvasNode = {
  key: 'steps[1]',
  serverName: 'build',
  label: 'build',
  kind: 'run',
  lane: 'main',
  issues: { errors: 0, warnings: 0 },
  run: {
    status: NodeStatus.Success,
    statusLabel: 'succeeded',
    retryCount: 0,
    doneCount: 0,
  },
};
const handler: CanvasNode = {
  ...step,
  key: 'handler_on.failure',
  serverName: 'onFailure',
  label: 'onFailure',
  lane: 'handler',
};
const agent: CanvasNode = {
  key: AGENT_NODE_KEY,
  label: 'Agent',
  kind: 'agent',
  lane: 'main',
  issues: { errors: 0, warnings: 0 },
};
const dependsEdge: CanvasEdge = {
  id: 'depends:steps[0]->steps[1]',
  source: 'steps[0]',
  target: 'steps[1]',
  kind: 'depends',
};
const openEdge: ContextMenuEdge = {
  edge: dependsEdge,
  insertable: true,
  lockReason: null,
};

const allowAll: StepCommandGates = {
  canRunStep: true,
  canRetry: () => true,
  canSetStatus: () => true,
  hasSubRuns: () => true,
};
const allowNone: StepCommandGates = {
  canRunStep: false,
  canRetry: () => false,
  canSetStatus: () => false,
  hasSubRuns: () => false,
};

function gates(over: Partial<ContextMenuGates> = {}): ContextMenuGates {
  return {
    editable: true,
    stepCommandGates: allowAll,
    clipboard: { canPaste: false },
    direction: 'LR',
    canChangeDirection: true,
    ...over,
  };
}

function ids(rows: { id: ContextMenuRowId }[]): ContextMenuRowId[] {
  return rows.map((row) => row.id);
}

const EDIT_ROWS: ContextMenuRowId[] = [
  'insertStep',
  'removeDependency',
  'addStep',
  'paste',
];

function allRows(g: ContextMenuGates): ContextMenuRowId[] {
  return [
    ...ids(contextMenuItems('node', step, g)),
    ...ids(contextMenuItems('edge', openEdge, g)),
    ...ids(contextMenuItems('pane', null, g)),
  ];
}

// parity:steps-actions-menu
describe('contextMenuItems', () => {
  // Open, Run step, Duplicate, Copy and Delete are on the hover toolbar and
  // have their keys: the menu has no row for them.
  it('lists the rows of a step for a user who may do all', () => {
    expect(ids(contextMenuItems('node', step, gates()))).toEqual([
      'viewLogs',
      'openSubRun',
      'retryStep',
      'setStatus',
      'addStep',
    ]);
  });

  it('has no copy or duplicate row in any menu', () => {
    for (const editable of [true, false]) {
      const seen = allRows(gates({ editable }));
      expect(seen).not.toContain('copy' as ContextMenuRowId);
      expect(seen).not.toContain('duplicate' as ContextMenuRowId);
    }
  });

  it('shows each row with its key', () => {
    const rows = contextMenuItems('node', step, gates());
    const keyOf = (id: ContextMenuRowId) =>
      rows.find((row) => row.id === id)?.shortcut;
    expect(keyOf('addStep')).toBe('N');
    const pane = contextMenuItems('pane', null, gates());
    expect(pane.find((row) => row.id === 'fitView')?.shortcut).toBe('F');
    expect(pane.find((row) => row.id === 'keyHelp')?.shortcut).toBe('Shift+?');
  });

  it('needs editable for every edit row', () => {
    const editable = allRows(gates());
    for (const id of EDIT_ROWS) {
      expect(editable).toContain(id);
    }
    const locked = allRows(gates({ editable: false }));
    for (const id of EDIT_ROWS) {
      expect(locked).not.toContain(id);
    }
  });

  it.each([
    ['retryStep', { ...allowAll, canRetry: () => false }],
    ['setStatus', { ...allowAll, canSetStatus: () => false }],
    ['openSubRun', { ...allowAll, hasSubRuns: () => false }],
  ] as const)('hides %s when its gate is closed', (id, run) => {
    expect(ids(contextMenuItems('node', step, gates()))).toContain(id);
    expect(
      ids(contextMenuItems('node', step, gates({ stepCommandGates: run })))
    ).not.toContain(id);
  });

  it('asks the gates with the server name of the step', () => {
    const asked: string[] = [];
    contextMenuItems(
      'node',
      step,
      gates({
        stepCommandGates: {
          canRunStep: true,
          canRetry: (name) => (asked.push(`retry:${name}`), true),
          canSetStatus: (name) => (asked.push(`status:${name}`), true),
          hasSubRuns: (name) => (asked.push(`sub:${name}`), true),
        },
      })
    );
    expect(asked.sort()).toEqual(['retry:build', 'status:build', 'sub:build']);
  });

  it('hides the run rows when the host takes no step command', () => {
    const rows = ids(
      contextMenuItems('node', step, gates({ stepCommandGates: undefined }))
    );
    for (const id of [
      'retryStep',
      'setStatus',
      'openSubRun',
      'viewLogs',
    ] as const) {
      expect(rows).not.toContain(id);
    }
  });

  it('never shows run, retry or status on a handler', () => {
    const rows = ids(contextMenuItems('node', handler, gates()));
    expect(rows).not.toContain('retryStep');
    expect(rows).not.toContain('setStatus');
    expect(rows).not.toContain('addStep');
    expect(rows).toContain('viewLogs');
  });

  it('gives the agent node no edit row', () => {
    const rows = ids(contextMenuItems('node', agent, gates()));
    expect(rows).not.toContain('addStep');
  });

  it('shows view logs only for a step with a run', () => {
    const noRun = { ...step, run: undefined };
    expect(ids(contextMenuItems('node', noRun, gates()))).not.toContain(
      'viewLogs'
    );
  });

  // parity:dup-12
  it('shows a read-only user only the rows that change nothing', () => {
    const g = gates({ editable: false, stepCommandGates: allowNone });
    const seen = new Set(allRows(g));
    expect([...seen].sort()).toEqual(
      [
        'viewLogs',
        'fitView',
        'directionLR',
        'directionTB',
        'exportSvg',
        'exportPng',
        'keyHelp',
      ].sort()
    );
  });

  // parity:canvas-keymap
  it('enables paste when the canvas is editable and holds a step', () => {
    const pane = contextMenuItems(
      'pane',
      null,
      gates({ clipboard: { canPaste: true } })
    );
    expect(pane.find((r) => r.id === 'paste')).toEqual({
      id: 'paste',
      label: 'Paste',
      shortcut: 'Ctrl/Cmd+V',
      disabled: false,
      section: 'edit',
    });
  });

  it('disables paste while there is nothing to paste', () => {
    for (const clipboard of [{ canPaste: false }, undefined]) {
      const pane = contextMenuItems('pane', null, gates({ clipboard }));
      expect(pane.find((r) => r.id === 'paste')).toMatchObject({
        disabled: true,
        reason: 'Nothing to paste',
      });
    }
  });

  it('marks the current direction and hides it without a handler', () => {
    const pane = contextMenuItems('pane', null, gates({ direction: 'TB' }));
    expect(pane.find((r) => r.id === 'directionTB')?.checked).toBe(true);
    expect(pane.find((r) => r.id === 'directionLR')?.checked).toBe(false);
    expect(
      ids(contextMenuItems('pane', null, gates({ canChangeDirection: false })))
    ).not.toContain('directionLR');
  });

  it('disables the removal of a locked edge with its reason', () => {
    const rows = contextMenuItems(
      'edge',
      { edge: dependsEdge, insertable: false, lockReason: 'Locked' },
      gates()
    );
    expect(rows).toEqual([
      {
        id: 'removeDependency',
        label: 'Remove dependency',
        shortcut: 'Delete',
        disabled: true,
        reason: 'Locked',
        section: 'edit',
      },
    ]);
  });
});
