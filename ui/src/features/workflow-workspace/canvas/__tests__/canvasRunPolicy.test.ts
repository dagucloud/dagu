// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import type { ApiDAGRunDetails } from '../../contracts';
import {
  canvasRunSource,
  draftStepIdentity,
  useCanvasRunSource,
  useDraftStepIdentity,
  withStableSteps,
} from '../canvasRunPolicy';

function node(name: string, id?: string) {
  return {
    step: { name, ...(id ? { id } : {}) },
    status: NodeStatus.Success,
    statusLabel: 'succeeded',
    retryCount: 0,
    doneCount: 1,
  };
}

function details(
  runId: string,
  nodes = [node('a'), node('cmd_2')]
): ApiDAGRunDetails {
  return {
    name: 'example',
    dagRunId: runId,
    status: Status.Success,
    nodes,
  } as unknown as ApiDAGRunDetails;
}

afterEach(() => {
  vi.useRealTimers();
});

describe('canvasRunSource', () => {
  it('is the selected run of the workspace', () => {
    const d = details('run-3');
    expect(
      canvasRunSource({ ref: { name: 'example', runId: 'run-3' }, details: d })
    ).toEqual({ runId: 'run-3', details: d });
  });

  it('has no run before the workflow ran', () => {
    expect(canvasRunSource({ ref: null, details: null })).toEqual({
      runId: null,
      details: null,
    });
  });

  // The id is known before the details of a pinned run have loaded.
  it('gives the id while the details load', () => {
    expect(
      canvasRunSource({
        ref: { name: 'example', runId: 'run-3' },
        details: null,
      })
    ).toEqual({ runId: 'run-3', details: null });
  });
});

describe('draftStepIdentity', () => {
  it('reads the explicit names and the ids', () => {
    const identity = draftStepIdentity(
      'steps:\n  - name: a\n    run: x\n  - id: b\n    run: y\n  - run: z\n'
    );
    expect([...(identity?.names ?? [])]).toEqual(['a']);
    expect([...(identity?.ids ?? [])]).toEqual(['b']);
  });

  it('is null for a text with no readable step', () => {
    expect(draftStepIdentity('foo: [unclosed')).toBeNull();
  });
});

describe('withStableSteps', () => {
  const source = { runId: 'run-1', details: details('run-1') };

  it('keeps the same source without a draft', () => {
    expect(withStableSteps(source, null)).toBe(source);
  });

  it('keeps the same source when every step is named', () => {
    const identity = { names: new Set(['a', 'cmd_2']), ids: new Set<string>() };
    expect(withStableSteps(source, identity)).toBe(source);
  });

  it('drops the steps that the draft does not name', () => {
    const identity = { names: new Set(['a']), ids: new Set<string>() };
    const next = withStableSteps(source, identity);
    expect(next.runId).toBe('run-1');
    expect(next.details?.nodes.map((n) => n.step.name)).toEqual(['a']);
    // The run itself is not changed.
    expect(source.details.nodes).toHaveLength(2);
  });

  it('keeps a step that the draft gives an id', () => {
    const s = { runId: 'run-1', details: details('run-1', [node('x', 'ex')]) };
    const identity = { names: new Set<string>(), ids: new Set(['ex']) };
    expect(withStableSteps(s, identity)).toBe(s);
  });

  it('passes a source with no details', () => {
    const empty = { runId: null, details: null };
    const identity = { names: new Set<string>(), ids: new Set<string>() };
    expect(withStableSteps(empty, identity)).toBe(empty);
  });
});

describe('useDraftStepIdentity', () => {
  it('is null while the draft is clean', () => {
    const handle = { getBuffer: vi.fn(() => 'steps:\n  - name: a\n') };
    const { result } = renderHook(() => useDraftStepIdentity(handle, false));
    expect(result.current).toBeNull();
    expect(handle.getBuffer).not.toHaveBeenCalled();
  });

  it('follows the text of the draft and parses it only when it changed', () => {
    vi.useFakeTimers();
    let text = 'steps:\n  - name: a\n    run: x\n';
    const handle = { getBuffer: vi.fn(() => text) };
    const { result } = renderHook(() => useDraftStepIdentity(handle, true));
    expect([...(result.current?.names ?? [])]).toEqual(['a']);
    const first = result.current;

    // The same text: the same object comes back.
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current).toBe(first);

    text = 'steps:\n  - name: a\n    run: x\n  - name: b\n    run: y\n';
    act(() => {
      vi.advanceTimersByTime(600);
    });
    expect([...(result.current?.names ?? [])]).toEqual(['a', 'b']);
  });

  // A change of the command alone leaves the steps as they are.
  it('keeps the object when the steps do not change', () => {
    vi.useFakeTimers();
    let text = 'steps:\n  - name: a\n    run: x\n';
    const handle = { getBuffer: vi.fn(() => text) };
    const { result } = renderHook(() => useDraftStepIdentity(handle, true));
    const first = result.current;
    text = 'steps:\n  - name: a\n    run: other\n';
    act(() => {
      vi.advanceTimersByTime(600);
    });
    expect(result.current).toBe(first);
  });

  it('is null again when the draft is saved', () => {
    const handle = { getBuffer: () => 'steps:\n  - name: a\n' };
    const { result, rerender } = renderHook(
      ({ dirty }) => useDraftStepIdentity(handle, dirty),
      { initialProps: { dirty: true } }
    );
    expect(result.current).not.toBeNull();
    rerender({ dirty: false });
    expect(result.current).toBeNull();
  });
});

describe('useCanvasRunSource', () => {
  const ref = (runId: string) => ({ name: 'example', runId });

  it('gives the run at once and holds the next details for one second', () => {
    vi.useFakeTimers();
    const first = details('run-1');
    const { result, rerender } = renderHook(
      ({ d }) =>
        useCanvasRunSource({ ref: ref('run-1'), details: d }, null, false),
      { initialProps: { d: first } }
    );
    expect(result.current.details).toBe(first);
    act(() => {
      vi.advanceTimersByTime(10);
    });

    const second = details('run-1');
    rerender({ d: second });
    act(() => {
      vi.advanceTimersByTime(100);
    });
    expect(result.current.details).toBe(first);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current.details).toBe(second);
  });

  // The details of the old run never sit under the id of the new run.
  it('shows no details of the old run under a new run id', () => {
    vi.useFakeTimers();
    const { result, rerender } = renderHook(
      ({ id, d }) =>
        useCanvasRunSource({ ref: ref(id), details: d }, null, false),
      {
        initialProps: {
          id: 'run-1',
          d: details('run-1') as ApiDAGRunDetails | null,
        },
      }
    );
    rerender({ id: 'run-2', d: null });
    expect(result.current).toEqual({ runId: 'run-2', details: null });
    const next = details('run-2');
    rerender({ id: 'run-2', d: next });
    act(() => {
      vi.advanceTimersByTime(10);
    });
    expect(result.current).toEqual({ runId: 'run-2', details: next });
  });

  // Spike case h: an unnamed step inserted in the draft.
  it('drops the status of an unnamed step when the draft changes', () => {
    const d = details('run-1', [node('a'), node('cmd_2'), node('cmd_3')]);
    const handle = {
      getBuffer: () =>
        'steps:\n  - run: new\n  - name: a\n    run: x\n  - run: y\n  - run: z\n',
    };
    const { result, rerender } = renderHook(
      ({ dirty }) =>
        useCanvasRunSource({ ref: ref('run-1'), details: d }, handle, dirty),
      { initialProps: { dirty: false } }
    );
    expect(result.current.details?.nodes).toHaveLength(3);
    rerender({ dirty: true });
    expect(result.current.details?.nodes.map((n) => n.step.name)).toEqual([
      'a',
    ]);
    expect(result.current.runId).toBe('run-1');
  });
});
