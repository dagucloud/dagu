// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { VisualEditorHandle } from '@/features/visual-editor/contracts';
import {
  NO_RUN_GATES,
  runStepGates,
  useStepCommandHost,
} from '../useStepCommandHost';
import { useDeepLinkReveal } from '../useStepViewHost';

function makeActions() {
  return {
    openSubRun: vi.fn(),
    setStatus: vi.fn(),
    retryStep: vi.fn(),
    runStep: vi.fn(),
    runUpstream: vi.fn(),
    openLogs: vi.fn(),
  };
}

const gates = {
  canRunStep: true,
  ...NO_RUN_GATES,
};
const openGates = {
  canRunStep: true,
  canRetry: () => true,
  canSetStatus: () => true,
  hasSubRuns: () => true,
};

describe('useStepCommandHost', () => {
  it('sends each command to its own action', () => {
    const actions = makeActions();
    const { result } = renderHook(() => useStepCommandHost(actions, openGates));
    const { onStepCommand } = result.current;

    onStepCommand({ kind: 'openSubRun', step: 'a', newTab: true });
    expect(actions.openSubRun).toHaveBeenCalledWith('a', { newTab: true });
    onStepCommand({ kind: 'setStatus', step: 'b' });
    expect(actions.setStatus).toHaveBeenCalledWith('b');
    onStepCommand({ kind: 'retryStep', step: 'c' });
    expect(actions.retryStep).toHaveBeenCalledWith('c');
    onStepCommand({ kind: 'runStep', step: 'd' });
    expect(actions.runStep).toHaveBeenCalledWith('d');
    onStepCommand({ kind: 'runUpstream', step: 'e' });
    expect(actions.runUpstream).toHaveBeenCalledWith('e');
    onStepCommand({ kind: 'viewLogs', step: 'f', stream: 'stderr' });
    expect(actions.openLogs).toHaveBeenCalledWith({
      step: 'f',
      stream: 'stderr',
    });

    // One command reaches one action only.
    expect(actions.openSubRun).toHaveBeenCalledTimes(1);
    expect(actions.runStep).toHaveBeenCalledTimes(1);
    expect(actions.runUpstream).toHaveBeenCalledTimes(1);
  });

  it('ignores a command that its gate refuses', () => {
    const actions = makeActions();
    const { result } = renderHook(() => useStepCommandHost(actions, gates));
    const { onStepCommand } = result.current;
    onStepCommand({ kind: 'openSubRun', step: 'a' });
    onStepCommand({ kind: 'setStatus', step: 'a' });
    onStepCommand({ kind: 'retryStep', step: 'a' });
    expect(actions.openSubRun).not.toHaveBeenCalled();
    expect(actions.setStatus).not.toHaveBeenCalled();
    expect(actions.retryStep).not.toHaveBeenCalled();
  });

  it('keeps the same handler while the actions stay', () => {
    const actions = makeActions();
    const { result, rerender } = renderHook(() =>
      useStepCommandHost(actions, gates)
    );
    const first = result.current.onStepCommand;
    rerender();
    expect(result.current.onStepCommand).toBe(first);
  });

  it('gives the gates of the host, which decide the offered commands', () => {
    const actions = makeActions();
    const { result } = renderHook(() => useStepCommandHost(actions, gates));
    expect(result.current.stepCommandGates).toBe(gates);
  });
});

describe('runStepGates', () => {
  const can = vi.fn((step: string) => ({
    retry: step === 'a',
    setStatus: step === 'b',
    runStep: true,
    subRuns: step === 'c',
  }));

  it('follows the actions of the step', () => {
    const g = runStepGates({ can }, { canExecute: true });
    expect(g.canRunStep).toBe(true);
    expect([g.canRetry('a'), g.canRetry('b')]).toEqual([true, false]);
    expect([g.canSetStatus('a'), g.canSetStatus('b')]).toEqual([false, true]);
    expect([g.hasSubRuns('a'), g.hasSubRuns('c')]).toEqual([false, true]);
  });

  it('hides Run step without the right to execute', () => {
    expect(runStepGates({ can }, { canExecute: false }).canRunStep).toBe(false);
  });

  it('offers no retry, status or sub-run without a run', () => {
    expect(NO_RUN_GATES.canRetry()).toBe(false);
    expect(NO_RUN_GATES.canSetStatus()).toBe(false);
    expect(NO_RUN_GATES.hasSubRuns()).toBe(false);
  });
});

describe('useDeepLinkReveal', () => {
  beforeEach(() => sessionStorage.clear());

  function handle(openForm: boolean) {
    return {
      openForm: vi.fn(() => openForm),
      openYaml: vi.fn(() => openForm),
      revealStep: vi.fn(() => true),
    } as unknown as VisualEditorHandle;
  }

  // The step view of a link opens on the YAML, and on the form when the
  // form was the last mode of the session.
  it('opens the step view in the YAML when the link names one', () => {
    const h = handle(true);
    renderHook(() =>
      useDeepLinkReveal(h, { step: 'a', ndv: 'expanded' }, true)
    );
    expect(h.openYaml).toHaveBeenCalledWith('a');
    expect(h.openForm).not.toHaveBeenCalled();
    expect(h.revealStep).not.toHaveBeenCalled();
  });

  it('opens the step view in the form after the form', () => {
    sessionStorage.setItem('dagu.stepView.mode', 'form');
    const h = handle(true);
    renderHook(() =>
      useDeepLinkReveal(h, { step: 'a', ndv: 'expanded' }, true)
    );
    expect(h.openForm).toHaveBeenCalledWith('a');
    expect(h.openYaml).not.toHaveBeenCalled();
  });

  it('reveals the step when there is no step view, and only once', () => {
    const h = handle(false);
    const { rerender } = renderHook(
      ({ target }: { target: VisualEditorHandle | null }) =>
        useDeepLinkReveal(target, { step: 'a', ndv: null }, true),
      { initialProps: { target: null as VisualEditorHandle | null } }
    );
    rerender({ target: h });
    rerender({ target: handle(false) });
    expect(h.revealStep).toHaveBeenCalledTimes(1);
    expect(h.revealStep).toHaveBeenCalledWith('a');
  });

  it('does not reveal on a canvas that has no step to scroll to', () => {
    const h = handle(false);
    renderHook(() => useDeepLinkReveal(h, { step: 'a', ndv: null }, false));
    expect(h.revealStep).not.toHaveBeenCalled();
  });
});
