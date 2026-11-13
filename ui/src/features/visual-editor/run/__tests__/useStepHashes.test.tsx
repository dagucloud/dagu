// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ApiDAGDetails } from '../../contracts';
import { parseDagDocLenient } from '../../doc';
import { useLiveStepHashes, useRunStepHashes } from '../useStepHashes';

// `stepHashes` shares one result between the calls with the same text and
// details. Each parse of a text is one miss of that cache.
vi.mock('../../doc', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../doc')>();
  return { ...actual, parseDagDocLenient: vi.fn(actual.parseDagDocLenient) };
});
const h = vi.hoisted(() => ({ runSpec: undefined as string | undefined }));
vi.mock('@/hooks/api', () => ({
  useQuery: (_path: string, init: unknown) => ({
    data:
      init !== null && h.runSpec !== undefined
        ? { spec: h.runSpec }
        : undefined,
  }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

let serial = 0;
/** A chain of `n` steps. Each call gives a text that no other test used. */
function chain(n: number): string {
  serial += 1;
  const steps = Array.from(
    { length: n },
    (_, i) => `  - name: s${i}\n    run: echo ${serial}\n    output: O${i}\n`
  );
  return `steps:\n${steps.join('')}`;
}

// The upstream panel mounts two hooks for each row, all on the same text.
function mountRows(text: string, rows: number, dag?: ApiDAGDetails) {
  return Array.from({ length: rows }, () =>
    renderHook(() =>
      useLiveStepHashes({
        fileName: 'wf.yaml',
        liveText: text,
        dag,
        enabled: true,
      })
    )
  );
}

beforeEach(() => {
  h.runSpec = undefined;
  vi.mocked(parseDagDocLenient).mockClear();
});

describe('useLiveStepHashes', () => {
  it('parses a text once for all the rows that read it', () => {
    const text = chain(30);
    const rows = mountRows(text, 30);
    expect(parseDagDocLenient).toHaveBeenCalledTimes(1);
    const first = rows[0]?.result.current;
    expect(first?.size).toBe(30);
    for (const row of rows) expect(row.result.current).toBe(first);
  });

  it('parses again for another text and for other details', () => {
    const a = chain(3);
    const b = chain(3);
    const dag = { name: 'wf' } as ApiDAGDetails;
    const other = { name: 'wf' } as ApiDAGDetails;
    mountRows(a, 2);
    mountRows(b, 2);
    mountRows(a, 2, dag);
    mountRows(a, 2, dag);
    mountRows(a, 2, other);
    expect(parseDagDocLenient).toHaveBeenCalledTimes(4);
  });

  it('holds a bounded number of texts', () => {
    const first = chain(2);
    mountRows(first, 1);
    for (let i = 0; i < 40; i++) mountRows(chain(2), 1);
    vi.mocked(parseDagDocLenient).mockClear();
    mountRows(first, 1);
    expect(parseDagDocLenient).toHaveBeenCalledTimes(1);
  });
});

describe('the live and the run hashes', () => {
  it('share one parse when the saved text and the run text are equal', () => {
    const text = chain(5);
    h.runSpec = text;
    const live = renderHook(() =>
      useLiveStepHashes({ fileName: 'wf.yaml', liveText: text, enabled: true })
    );
    const run = renderHook(() =>
      useRunStepHashes({ dagName: 'wf', runId: 'r1', enabled: true })
    );
    expect(parseDagDocLenient).toHaveBeenCalledTimes(1);
    expect(run.result.current).toBe(live.result.current);
  });
});
