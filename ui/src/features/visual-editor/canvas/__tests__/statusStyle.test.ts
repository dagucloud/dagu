// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { NodeStatus } from '@/api/v1/schema';
import { describe, expect, it } from 'vitest';
import {
  edgeTintStatus,
  statusBorderClass,
  statusCardClass,
  statusDashArray,
  statusStrokeClass,
} from '../statusStyle';

// The graph shows a status by the --graph-* tokens. Each one has the value of
// the --status-* token of its theme.
describe('status classes', () => {
  it.each([
    [NodeStatus.Success, '--graph-success'],
    [NodeStatus.Running, '--graph-running'],
    [NodeStatus.Retrying, '--graph-warning'],
    [NodeStatus.Waiting, '--graph-warning'],
    [NodeStatus.PartialSuccess, '--graph-warning'],
    [NodeStatus.Failed, '--graph-error'],
    [NodeStatus.Rejected, '--graph-error'],
    // parity:graph-status-colors parity:dup-23
    [NodeStatus.Aborted, '--graph-aborted'],
    [NodeStatus.NotStarted, '--graph-neutral'],
    [NodeStatus.Skipped, '--graph-neutral'],
  ])('maps status %s to %s', (status, token) => {
    expect(statusBorderClass(status).split(' ')).toEqual([
      `border-[var(${token})]`,
      `dark:border-[var(${token})]`,
    ]);
    expect(statusStrokeClass(status)).toBe(`stroke-[var(${token})]`);
  });

  // One value in the light theme, then one in the dark theme.
  it('defines each graph token in both themes', () => {
    const css = fs.readFileSync(
      path.resolve(
        path.dirname(fileURLToPath(import.meta.url)),
        '../../../../styles/global.css'
      ),
      'utf8'
    );
    const values = (token: string) =>
      [...css.matchAll(new RegExp(`\\s${token}:\\s*([^;]+);`, 'g'))].map(
        (m) => m[1]
      );
    expect(values('--graph-success')).toEqual(['#178a44', '#22c55e']);
    expect(values('--graph-error')).toEqual(['#c8382f', '#ef5350']);
    expect(values('--graph-running')).toEqual(['#5d4fe0', '#a89ff8']);
    expect(values('--graph-warning')).toEqual(['#9a5d0b', '#d9a03c']);
    expect(values('--graph-aborted')).toEqual(['#a8208a', '#e57ac8']);
    expect(values('--graph-neutral')).toEqual(['#80808b', '#8a8d99']);
  });
});

// The status of a step of the run reads at any zoom: a line of two widths
// and a tint of the card.
describe('statusCardClass', () => {
  it.each([
    [NodeStatus.Success, '--graph-success'],
    [NodeStatus.Failed, '--graph-error'],
    [NodeStatus.Running, '--graph-running'],
    [NodeStatus.Waiting, '--graph-warning'],
    [NodeStatus.Aborted, '--graph-aborted'],
  ])('rings and tints the card of status %s', (status, token) => {
    expect(statusCardClass(status).split(' ')).toEqual(
      expect.arrayContaining([
        `border-[var(${token})]`,
        'ring-1',
        `ring-[var(${token})]`,
        `bg-[color-mix(in_srgb,var(${token})_10%,var(--card))]`,
      ])
    );
  });

  it.each([NodeStatus.NotStarted, NodeStatus.Skipped])(
    'keeps the plain card of a step that did not run (%s)',
    (status) => {
      expect(statusCardClass(status)).toBe(statusBorderClass(status));
    }
  );
});

describe('statusDashArray', () => {
  it('dashes the edges of a failed step only', () => {
    expect(statusDashArray(NodeStatus.Failed)).toBeDefined();
    expect(statusDashArray(NodeStatus.Rejected)).toBeDefined();
    expect(statusDashArray(NodeStatus.Success)).toBeUndefined();
    expect(statusDashArray(NodeStatus.Running)).toBeUndefined();
  });
});

describe('edgeTintStatus', () => {
  it('takes the status of the source', () => {
    expect(edgeTintStatus(NodeStatus.Success, NodeStatus.Success)).toBe(
      NodeStatus.Success
    );
    expect(edgeTintStatus(NodeStatus.Failed, NodeStatus.Running)).toBe(
      NodeStatus.Failed
    );
  });

  // A step added after the run has no status: its edge is not part of the run.
  it('has no tint into a step that is not in the run', () => {
    expect(edgeTintStatus(NodeStatus.Success, undefined)).toBeUndefined();
  });

  it('is neutral into a skipped or not started target', () => {
    for (const target of [NodeStatus.Skipped, NodeStatus.NotStarted]) {
      const tint = edgeTintStatus(NodeStatus.Success, target);
      expect(statusStrokeClass(tint!)).toBe(
        statusStrokeClass(NodeStatus.Skipped)
      );
    }
  });

  it('has no tint without a run', () => {
    expect(edgeTintStatus(undefined, NodeStatus.Skipped)).toBeUndefined();
  });
});
