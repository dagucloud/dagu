// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The UI keeps its clocks on one shared timer for each period
// (hooks/useClock.ts). This scan fails on a new `setInterval(` in src outside
// that file, so that a component never owns a timer of its own again. Polls of
// data go through SWR or `subscribeClock`.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const srcDir = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../..'
);

/** Files that may call `setInterval`, as paths relative to src. */
const ALLOWED = new Set(['hooks/useClock.ts']);

const RAW_INTERVAL = /\bsetInterval\s*\(/;

/** True when a source text calls `setInterval`. */
export function callsSetInterval(source: string): boolean {
  return RAW_INTERVAL.test(source);
}

function sources(dir: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== '__tests__' && entry.name !== 'node_modules') {
        out.push(...sources(full));
      }
    } else if (/\.(ts|tsx)$/.test(entry.name) && !/\.test\./.test(entry.name)) {
      out.push(full);
    }
  }
  return out;
}

describe('shared clock guard', () => {
  it('finds a call in a probe', () => {
    expect(callsSetInterval('const id = setInterval(tick, 1000);')).toBe(true);
    expect(callsSetInterval('window.setInterval (tick, 1000)')).toBe(true);
    expect(callsSetInterval('clearInterval(id)')).toBe(false);
    expect(callsSetInterval('const setIntervalMs = 5')).toBe(false);
  });

  it('keeps every setInterval in the shared clock', () => {
    const hits = sources(srcDir)
      .map((file) => path.relative(srcDir, file).split(path.sep).join('/'))
      .filter((rel) => !ALLOWED.has(rel))
      .filter((rel) =>
        callsSetInterval(fs.readFileSync(path.join(srcDir, rel), 'utf8'))
      );
    expect(hits).toEqual([]);
  });
});
