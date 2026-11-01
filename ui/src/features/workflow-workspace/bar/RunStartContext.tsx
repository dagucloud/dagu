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
import { AlertTriangle } from 'lucide-react';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { useUnsavedChanges } from '@/contexts/UnsavedChangesContext';
import { DAGContext } from '@/features/dags/contexts/DAGContext';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';

export interface RunStartController {
  /** canRunDags: the gate of the Start dialog. */
  canStart: boolean;
  /** Opens the start dialog of the single Start owner. */
  requestStart(): void;
}

interface RunStartState {
  pending: boolean;
  request(): void;
  consume(): void;
  /** Returns the unregister function. */
  registerTarget(): () => void;
}

const RunStartStateContext = createContext<RunStartState | null>(null);

/** Lets any zone of the workspace open the Start dialog of the bar. */
export function RunStartProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState(false);
  const targets = useRef(0);
  const request = useCallback(() => {
    // Without a mounted target, a request would open the dialog later.
    if (targets.current > 0) setPending(true);
  }, []);
  const consume = useCallback(() => setPending(false), []);
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
  const request = state?.request;
  return useMemo(
    () => ({
      canStart: canRunDags,
      requestStart: canRunDags && request ? request : noop,
    }),
    [canRunDags, request]
  );
}

/**
 * Wraps the single Start owner, the start-only DAGActions. A request sets
 * DAGContext.autoOpenStartModal for it; DAGActions opens its dialog in an
 * effect when the flag becomes true. The flag goes back to false after that
 * effect, so the next request opens the dialog again. With unsaved changes,
 * the request first shows the same guard as the Start button.
 */
export function RunStartTarget({ children }: { children: ReactNode }) {
  const state = useContext(RunStartStateContext);
  const parent = useContext(DAGContext);
  const { hasUnsavedChanges } = useUnsavedChanges();
  const { ts } = useI18n();
  const [guard, setGuard] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const pending = state?.pending ?? false;
  const registerTarget = state?.registerTarget;
  const consume = state?.consume;

  useEffect(() => registerTarget?.(), [registerTarget]);
  // Child effects run first, so DAGActions has seen the flag.
  useEffect(() => {
    if (!pending) return;
    if (hasUnsavedChanges) setGuard(true);
    consume?.();
  }, [pending, hasUnsavedChanges, consume]);
  useEffect(() => {
    if (confirmed) setConfirmed(false);
  }, [confirmed]);

  const open = (pending && !hasUnsavedChanges) || confirmed;
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
      <I18nProps>
        <ConfirmModal
          title={ts('Unsaved Changes')}
          buttonText="Run anyway"
          visible={guard}
          dismissModal={() => setGuard(false)}
          onSubmit={() => {
            setGuard(false);
            setConfirmed(true);
          }}
        >
          <div className="flex items-start gap-3">
            <AlertTriangle className="h-5 w-5 text-warning flex-shrink-0 mt-0.5" />
            <div className="space-y-2">
              <p className="font-medium">
                <I18nText
                  text={'You have unsaved changes in the DAG definition.'}
                />
              </p>
              <p className="text-sm text-muted-foreground">
                <I18nText
                  text={
                    'The DAG will run with the last saved version, not your current edits. Save your changes first if you want them to take effect.'
                  }
                />
              </p>
            </div>
          </div>
        </ConfirmModal>
      </I18nProps>
    </DAGContext.Provider>
  );
}
