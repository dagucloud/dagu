// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type RefCallback,
} from 'react';
import type { WorkspaceEditorHandle, WorkspaceEditorState } from '../contracts';

/** Every consumer null-checks handle. Commands that edit are disabled when !state?.editable || state.frozen. */
export interface WorkspaceEditorValue {
  handle: WorkspaceEditorHandle | null;
  state: WorkspaceEditorState | null;
}

export interface WorkspaceEditorBinding {
  editorHandleRef: RefCallback<WorkspaceEditorHandle>;
  onEditorStateChange(s: WorkspaceEditorState): void;
}

const EMPTY: WorkspaceEditorValue = { handle: null, state: null };

const ValueContext = createContext<WorkspaceEditorValue>(EMPTY);

interface Setters {
  setHandle(h: WorkspaceEditorHandle | null): void;
  setState(s: WorkspaceEditorState | null): void;
}
const SettersContext = createContext<Setters | null>(null);

export function WorkspaceEditorProvider({ children }: { children: ReactNode }) {
  const [handle, setHandle] = useState<WorkspaceEditorHandle | null>(null);
  const [state, setState] = useState<WorkspaceEditorState | null>(null);
  const setters = useMemo<Setters>(() => ({ setHandle, setState }), []);
  const value = useMemo(() => ({ handle, state }), [handle, state]);
  return (
    <SettersContext.Provider value={setters}>
      <ValueContext.Provider value={value}>{children}</ValueContext.Provider>
    </SettersContext.Provider>
  );
}

/** The bound editor's handle and state; both null before binding and outside a provider. */
export function useWorkspaceEditor(): WorkspaceEditorValue {
  return useContext(ValueContext);
}

/**
 * Binds the editor of the calling component: pass editorHandleRef and
 * onEditorStateChange to DAGSpec. When the component unmounts, handle and
 * state go back to null.
 */
export function useWorkspaceEditorBinding(): WorkspaceEditorBinding {
  const setters = useContext(SettersContext);
  // Kept so that a remount (StrictMode) publishes the last state again.
  const lastState = useRef<WorkspaceEditorState | null>(null);
  const editorHandleRef = useCallback<RefCallback<WorkspaceEditorHandle>>(
    (h) => {
      setters?.setHandle(h);
    },
    [setters]
  );
  const onEditorStateChange = useCallback(
    (s: WorkspaceEditorState) => {
      lastState.current = s;
      setters?.setState(s);
    },
    [setters]
  );
  useEffect(() => {
    if (lastState.current) setters?.setState(lastState.current);
    return () => {
      setters?.setHandle(null);
      setters?.setState(null);
    };
  }, [setters]);
  return useMemo(
    () => ({ editorHandleRef, onEditorStateChange }),
    [editorHandleRef, onEditorStateChange]
  );
}
