// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render } from '@testing-library/react';
import { useEffect, useImperativeHandle, useState, type Ref } from 'react';
import { describe, expect, it, vi } from 'vitest';

import type {
  WorkspaceEditorHandle,
  WorkspaceEditorState,
} from '../../contracts';
import {
  useWorkspaceEditor,
  useWorkspaceEditorBinding,
  WorkspaceEditorProvider,
  type WorkspaceEditorValue,
} from '../WorkspaceEditorContext';

const STATE: WorkspaceEditorState = {
  frozen: false,
  canUndo: false,
  canRedo: false,
  selectedStepName: null,
  drawer: 'none',
  direction: 'TB',
  loaded: true,
  editable: true,
  dirty: false,
  saving: false,
  saveState: 'clean',
  hasDraft: false,
  conflict: false,
};

function makeHandle(): WorkspaceEditorHandle {
  return {
    fitView: vi.fn(),
    revealStep: vi.fn(() => true),
    highlightStep: vi.fn(),
    addStep: vi.fn(),
    openWorkflowPanel: vi.fn(),
    openLauncher: vi.fn(),
    toggleYaml: vi.fn(),
    openForm: vi.fn(() => true),
    openYaml: vi.fn(() => true),
    copySelection: vi.fn(() => null),
    paste: vi.fn(() => ({ ok: false, code: 'noop', message: '' }) as const),
    duplicateSelection: vi.fn(
      () => ({ ok: false, code: 'noop', message: '' }) as const
    ),
    undo: vi.fn(),
    redo: vi.fn(),
    save: vi.fn(),
    discard: vi.fn(),
    replaceBuffer: vi.fn(),
    isDirty: vi.fn(() => false),
    getBuffer: vi.fn(() => ''),
    getDag: vi.fn(() => undefined),
    copyYaml: vi.fn(async () => {}),
  };
}

/** Stands in for DAGSpec: exposes a handle and reports its state. */
function FakeEditor({
  handleRef,
  onStateChange,
  handle,
  state,
}: {
  handleRef: Ref<WorkspaceEditorHandle>;
  onStateChange(s: WorkspaceEditorState): void;
  handle: WorkspaceEditorHandle;
  state: WorkspaceEditorState;
}) {
  useImperativeHandle(handleRef, () => handle, [handle]);
  useEffect(() => {
    onStateChange(state);
  }, [onStateChange, state]);
  return null;
}

function Binder({
  handle,
  state,
}: {
  handle: WorkspaceEditorHandle;
  state: WorkspaceEditorState;
}) {
  const { editorHandleRef, onEditorStateChange } = useWorkspaceEditorBinding();
  return (
    <FakeEditor
      handleRef={editorHandleRef}
      onStateChange={onEditorStateChange}
      handle={handle}
      state={state}
    />
  );
}

function lastOf<T>(values: T[]): T | undefined {
  return values[values.length - 1];
}

function Consumer({ seen }: { seen: WorkspaceEditorValue[] }) {
  seen.push(useWorkspaceEditor());
  return null;
}

describe('WorkspaceEditorContext', () => {
  it('gives the bound handle and state to consumers, and null after unmount', () => {
    const handle = makeHandle();
    const seen: WorkspaceEditorValue[] = [];
    let setBound: (bound: boolean) => void = () => {};
    let setEditorState: (s: WorkspaceEditorState) => void = () => {};

    function Harness() {
      const [bound, setB] = useState(false);
      const [state, setS] = useState(STATE);
      setBound = setB;
      setEditorState = setS;
      return (
        <WorkspaceEditorProvider>
          {bound && <Binder handle={handle} state={state} />}
          <Consumer seen={seen} />
        </WorkspaceEditorProvider>
      );
    }

    render(<Harness />);
    expect(lastOf(seen)).toStrictEqual({ handle: null, state: null });

    act(() => setBound(true));
    expect(lastOf(seen)?.handle).toBe(handle);
    expect(lastOf(seen)?.state).toStrictEqual(STATE);
    lastOf(seen)?.handle?.revealStep('build');
    expect(handle.revealStep).toHaveBeenCalledWith('build');

    const dirty = { ...STATE, dirty: true };
    act(() => setEditorState(dirty));
    expect(lastOf(seen)?.state).toBe(dirty);

    act(() => setBound(false));
    expect(lastOf(seen)).toStrictEqual({ handle: null, state: null });
  });

  it('gives null handle and state outside a provider', () => {
    const seen: WorkspaceEditorValue[] = [];
    render(<Consumer seen={seen} />);
    expect(lastOf(seen)).toStrictEqual({ handle: null, state: null });
  });

  it('accepts a binding outside a provider without effect', () => {
    const seen: WorkspaceEditorValue[] = [];
    render(
      <>
        <Binder handle={makeHandle()} state={STATE} />
        <Consumer seen={seen} />
      </>
    );
    expect(lastOf(seen)).toStrictEqual({ handle: null, state: null });
  });
});
