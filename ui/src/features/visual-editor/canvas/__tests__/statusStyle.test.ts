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
  statusStrokeClass,
} from '../statusStyle';

describe('status classes', () => {
  it.each([
    [NodeStatus.Success, 'success'],
    [NodeStatus.Running, '[var(--status-running)]'],
    [NodeStatus.Retrying, 'warning'],
    [NodeStatus.Waiting, 'warning'],
    [NodeStatus.PartialSuccess, 'warning'],
    [NodeStatus.Failed, 'destructive'],
    [NodeStatus.Rejected, 'destructive'],
    // parity:graph-status-colors parity:dup-23
    [NodeStatus.Aborted, '[var(--status-aborted)]'],
    [NodeStatus.NotStarted, '[var(--status-neutral)]'],
    [NodeStatus.Skipped, '[var(--status-neutral)]'],
  ])('maps status %s to %s', (status, token) => {
    // The colour is on the border of the card, in both themes.
    expect(statusBorderClass(status)).toBe(
      `border-${token} dark:border-${token}`
    );
  });

  it('gives the stroke of the same colour as the border', () => {
    for (const status of Object.values(NodeStatus).filter(
      (v): v is NodeStatus => typeof v === 'number'
    )) {
      const colour = statusBorderClass(status)
        .split(' ')[0]!
        .replace('border-', '');
      expect(statusStrokeClass(status)).toBe(`stroke-${colour}`);
    }
  });

  // Success, Failed and the warning statuses keep their theme utilities, so
  // these tokens must hold the colours of the status tokens. parity:dup-23
  it('keeps the theme utilities on the colours of the status tokens', () => {
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
    for (const [theme, status] of [
      ['--success', '--status-success'],
      ['--destructive', '--status-error'],
      ['--warning', '--status-warning'],
    ] as const) {
      expect(values(theme)).toHaveLength(2);
      expect(values(theme)).toEqual(values(status));
    }
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
    expect(edgeTintStatus(NodeStatus.Success, undefined)).toBe(
      NodeStatus.Success
    );
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
