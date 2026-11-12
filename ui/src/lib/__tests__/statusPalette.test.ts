// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import tailwindcss from '@tailwindcss/postcss';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import postcss, { type Root } from 'postcss';
import { describe, expect, it } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import {
  statusClasses,
  statusTone,
  toneClasses,
  type StatusTone,
} from '../statusPalette';
import { getStatusClass } from '../status-utils';

const TONES: StatusTone[] = [
  'success',
  'error',
  'running',
  'warning',
  'aborted',
  'neutral',
];

const TOKEN: Record<StatusTone, string> = {
  success: '--status-success',
  error: '--status-error',
  running: '--status-running',
  warning: '--status-warning',
  aborted: '--status-aborted',
  neutral: '--status-neutral',
};

/** The tone that each class of getStatusClass stands for. */
const TONE_OF_CLASS: Record<string, StatusTone> = {
  'status-success': 'success',
  'status-failed': 'error',
  'status-running': 'running',
  'status-warning': 'warning',
  'status-aborted': 'aborted',
  'status-neutral': 'neutral',
  'status-muted': 'neutral',
};

const RUN_ROWS: Array<[Status, StatusTone]> = [
  [Status.NotStarted, 'neutral'],
  [Status.Running, 'running'],
  [Status.Failed, 'error'],
  [Status.Aborted, 'aborted'],
  [Status.Success, 'success'],
  [Status.Queued, 'neutral'],
  [Status.PartialSuccess, 'warning'],
  [Status.Waiting, 'warning'],
  [Status.Rejected, 'error'],
];

const NODE_ROWS: Array<[NodeStatus, StatusTone]> = [
  [NodeStatus.NotStarted, 'neutral'],
  [NodeStatus.Running, 'running'],
  [NodeStatus.Failed, 'error'],
  [NodeStatus.Aborted, 'aborted'],
  [NodeStatus.Success, 'success'],
  [NodeStatus.Skipped, 'neutral'],
  [NodeStatus.PartialSuccess, 'warning'],
  [NodeStatus.Waiting, 'warning'],
  [NodeStatus.Rejected, 'error'],
  [NodeStatus.Retrying, 'warning'],
];

function enumValues<T>(e: Record<string, string | number>): T[] {
  return Object.values(e).filter((v) => typeof v === 'number') as T[];
}

// parity:dup-23
describe('statusTone', () => {
  it('has a row for every Status and NodeStatus value', () => {
    expect(RUN_ROWS.map(([s]) => s).sort()).toEqual(
      enumValues<Status>(Status).sort()
    );
    expect(NODE_ROWS.map(([s]) => s).sort()).toEqual(
      enumValues<NodeStatus>(NodeStatus).sort()
    );
  });

  it.each(RUN_ROWS)('gives run status %s the tone %s', (status, tone) => {
    expect(statusTone(status)).toBe(tone);
    expect(TONE_OF_CLASS[getStatusClass(status)]).toBe(tone);
  });

  it.each(NODE_ROWS)('gives step status %s the tone %s', (status, tone) => {
    expect(statusTone(status)).toBe(tone);
    expect(TONE_OF_CLASS[getStatusClass(status)]).toBe(tone);
  });

  it('gives neutral to a missing or unknown status', () => {
    expect(statusTone(undefined)).toBe('neutral');
    expect(statusTone(99 as Status)).toBe('neutral');
    expect(getStatusClass(99 as Status)).toBe('status-muted');
  });
});

describe('toneClasses', () => {
  it.each(TONES)('builds every %s class on its status token only', (tone) => {
    const classes = toneClasses(tone);
    expect(Object.keys(classes).sort()).toEqual([
      'bg',
      'dot',
      'ring',
      'stroke',
      'text',
    ]);
    for (const value of Object.values(classes)) {
      expect(value).toContain(`[var(${TOKEN[tone]})]`);
      expect(value.match(/var\(--[a-z-]+\)/g)).toEqual([`var(${TOKEN[tone]})`]);
    }
  });

  it('gives the classes of the tone of a status', () => {
    expect(statusClasses(Status.Aborted)).toBe(toneClasses('aborted'));
    expect(statusClasses(NodeStatus.Skipped)).toBe(toneClasses('neutral'));
  });

  it('generates a utility on the token for every class', async () => {
    const file = resolve('src/styles/global.css');
    const root: Root = (
      await postcss([tailwindcss()]).process(await readFile(file, 'utf8'), {
        from: file,
      })
    ).root;
    const rules = new Map<string, string>();
    root.walkRules((rule) => {
      rules.set(rule.selector, rule.toString());
    });

    for (const tone of TONES) {
      for (const utility of Object.values(toneClasses(tone))) {
        const selector = `.${utility.replace(/([:/[\]()=])/g, '\\$1')}`;
        expect(rules.get(selector), `${utility} is generated`).toContain(
          `var(${TOKEN[tone]})`
        );
      }
    }
  });
});
