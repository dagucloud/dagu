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
} from 'react';
import { useUnsavedChanges } from '@/contexts/UnsavedChangesContext';
import { UnsavedRunDialog } from '@/features/dags/components/common/UnsavedRunDialog';
import { DAGContext } from '@/features/dags/contexts/DAGContext';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';

export interface RunStartController {
  /** canRunDags: the gate of the Start dialog. */
  canStart: boolean;
  /** Opens the start dialog of the single Start owner. */
  requestStart(): void;
  /** canStart, and a mounted editor can save its edits. */
  canSaveAndStart: boolean;
  /**
   * Saves the edits of the editor, then opens the start dialog. A failed save
   * opens nothing.
   */
  requestSaveAndStart(): void;
}

type StartRequest = 'start' | 'saveAndStart';

interface RunStartState {
  pending: StartRequest | null;
  request(kind: StartRequest): void;
  consume(): void;
  /** Returns the unregister function. */
  registerTarget(): () => void;
}

const RunStartStateContext = createContext<RunStartState | null>(null);

/** Lets any zone of the workspace open the Start dialog of the bar. */
export function RunStartProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<StartRequest | null>(null);
  const targets = useRef(0);
  const request = useCallback((kind: StartRequest) => {
    // Without a mounted target, a request would open the dialog later.
    if (targets.current > 0) setPending(kind);
  }, []);
  const consume = useCallback(() => setPending(null), []);
  const registerTarget = useCallback(() => {
    targets.current += 1;
    return () => {
      targets.current -= 1;
    };
  }, []);
  const value = useMemo(
    () => ({ pending, request, consume, registerTarget }),
    [pending, request, consume, registerTarget]
  );
  return (
    <RunStartStateContext.Provider value={value}>
      {children}
    </RunStartStateContext.Provider>
  );
}

const noop = () => {};

/** Needs WorkspaceDataProvider. Outside RunStartProvider, requestStart does nothing. */
export function useRunStart(): RunStartController {
  const state = useContext(RunStartStateContext);
  const { canRunDags } = useWorkspacePermissions();
  const { canSave } = useUnsavedChanges();
  const request = state?.request;
  return useMemo(() => {
    const canSaveAndStart = canRunDags && canSave;
    return {
      canStart: canRunDags,
      requestStart: canRunDags && request ? () => request('start') : noop,
      canSaveAndStart,
      requestSaveAndStart:
        canSaveAndStart && request ? () => request('saveAndStart') : noop,
    };
  }, [canRunDags, canSave, request]);
}

/**
 * Wraps the single Start owner, the start-only DAGActions. A request sets
 * DAGContext.autoOpenStartModal for it; DAGActions opens its dialog in an
 * effect when the flag becomes true. The flag goes back to false after that
 * effect, so the next request opens the dialog again. With unsaved changes,
 * the request first shows the same guard as the Start button. A request to
 * save and start saves instead of the guard, and opens the dialog only after
 * a successful save.
 */
export function RunStartTarget({ children }: { children: ReactNode }) {
  const state = useContext(RunStartStateContext);
  const parent = useContext(DAGContext);
  const { hasUnsavedChanges, canSave, save } = useUnsavedChanges();
  const [guard, setGuard] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const pending = state?.pending ?? null;
  const registerTarget = state?.registerTarget;
  const consume = state?.consume;

  useEffect(() => registerTarget?.(), [registerTarget]);
  // Child effects run first, so DAGActions has seen the flag.
  const saveAndStart = useCallback(async () => {
    if (await save()) setConfirmed(true);
  }, [save]);
  useEffect(() => {
    if (!pending) return;
    if (pending === 'saveAndStart') void saveAndStart();
    else if (hasUnsavedChanges) setGuard(true);
    consume?.();
  }, [pending, hasUnsavedChanges, consume, saveAndStart]);
  useEffect(() => {
    if (confirmed) setConfirmed(false);
  }, [confirmed]);

  const open = (pending === 'start' && !hasUnsavedChanges) || confirmed;
  const value = useMemo(
    () => ({
      ...parent,
      autoOpenStartModal: Boolean(parent.autoOpenStartModal) || open,
    }),
    [parent, open]
  );
  return (
    <DAGContext.Provider value={value}>
      {children}
      <UnsavedRunDialog
        visible={guard}
        canSave={canSave}
        onDismiss={() => setGuard(false)}
        onRunSaved={() => {
          setGuard(false);
          setConfirmed(true);
        }}
        onSaveAndRun={() => {
          // The dialog closes first: a failed save shows its errors.
          setGuard(false);
          void saveAndStart();
        }}
      />
    </DAGContext.Provider>
  );
}
