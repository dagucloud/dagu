// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { useI18n } from '@/i18n/I18nProvider';
import {
  createContext,
  createElement,
  Fragment,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';

/** A section of the Workflow panel with a draft that is not saved. */
export type DirtySection = 'notifications' | 'incidents' | 'profile';

export interface SettingsLeaveGuard {
  setDirty(section: DirtySection, dirty: boolean): void;
  /** WebhookTab one-time secret shown. */
  setSecretVisible(visible: boolean): void;
  /** Runs next() at once when nothing is at risk; else opens a confirmation (Cancel focused first) and runs next() on confirm. */
  guard(next: () => void): void;
  /** The editor draft is not saved. Only guardLeave and the unload prompt look at it. */
  setEditorDirty(dirty: boolean): void;
  /**
   * Like guard, for a move that unmounts the workflow page (another route,
   * the run page): it also asks while the editor draft is not saved.
   */
  guardLeave(next: () => void): void;
  /** A settings section is at risk (the editor draft is not counted). */
  atRisk: boolean;
  /** The confirmation to render. Null in a SettingsLeaveGuardProvider, which renders it once. */
  confirmDialog: ReactNode;
}

interface Risk {
  notifications: boolean;
  incidents: boolean;
  profile: boolean;
  secret: boolean;
  editor: boolean;
}

const NO_RISK: Risk = {
  notifications: false,
  incidents: false,
  profile: false,
  secret: false,
  editor: false,
};

function isAtRisk(risk: Risk): boolean {
  return risk.notifications || risk.incidents || risk.profile || risk.secret;
}

function isAnyRisk(risk: Risk): boolean {
  return isAtRisk(risk) || risk.editor;
}

function useGuardController(): SettingsLeaveGuard {
  const { ts } = useI18n();
  const [risk, setRisk] = useState<Risk>(NO_RISK);
  // guard() can run in the same event as a setter, before a render.
  const riskRef = useRef<Risk>(NO_RISK);
  // Kept after close, so the text does not change while the dialog fades out.
  const [prompt, setPrompt] = useState<{
    next: () => void;
    secret: boolean;
    editor: boolean;
    leave: boolean;
    open: boolean;
  } | null>(null);

  const update = useCallback((patch: Partial<Risk>) => {
    const next = { ...riskRef.current, ...patch };
    if (
      next.notifications === riskRef.current.notifications &&
      next.incidents === riskRef.current.incidents &&
      next.profile === riskRef.current.profile &&
      next.secret === riskRef.current.secret &&
      next.editor === riskRef.current.editor
    ) {
      return;
    }
    riskRef.current = next;
    setRisk(next);
  }, []);

  const setDirty = useCallback(
    (section: DirtySection, dirty: boolean) => update({ [section]: dirty }),
    [update]
  );
  const setSecretVisible = useCallback(
    (visible: boolean) => update({ secret: visible }),
    [update]
  );
  const setEditorDirty = useCallback(
    (dirty: boolean) => update({ editor: dirty }),
    [update]
  );
  const ask = useCallback((next: () => void, leave: boolean) => {
    const current = riskRef.current;
    if (leave ? isAnyRisk(current) : isAtRisk(current)) {
      setPrompt({
        next,
        secret: current.secret,
        editor: leave && current.editor,
        leave,
        open: true,
      });
    } else {
      next();
    }
  }, []);
  const guard = useCallback((next: () => void) => ask(next, false), [ask]);
  const guardLeave = useCallback((next: () => void) => ask(next, true), [ask]);

  const atRisk = isAtRisk(risk);
  const anyRisk = isAnyRisk(risk);

  // A risk can go while the question is open, for example when browser Back
  // unmounts the section. Then there is nothing left to ask about. The
  // question of guard() does not cover the editor draft.
  const promptRisk = prompt?.leave ? anyRisk : atRisk;
  useEffect(() => {
    if (!promptRisk) {
      setPrompt((current) =>
        current?.open ? { ...current, open: false } : current
      );
    }
  }, [promptRisk]);

  useEffect(() => {
    if (!anyRisk) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [anyRisk]);

  const dismiss = useCallback(
    () => setPrompt((current) => current && { ...current, open: false }),
    []
  );
  const confirm = useCallback(() => {
    if (!prompt?.open) return;
    setPrompt({ ...prompt, open: false });
    // The user accepted the loss: what was at risk is gone. A guard that
    // stays on the page keeps the editor draft, which is still there.
    update(
      prompt.leave ? NO_RISK : { ...NO_RISK, editor: riskRef.current.editor }
    );
    prompt.next();
  }, [prompt, update]);

  const confirmDialog = createElement(ConfirmDialog, {
    title: ts('Leave without saving?'),
    buttonText: ts('Leave'),
    visible: !!prompt?.open,
    dismissModal: dismiss,
    onSubmit: confirm,
    // Above the settings dialog, which sits on Z.dialog too. The safe choice
    // has the focus, and it gets the focus back on close.
    layer: 'raised',
    initialFocus: 'cancel',
    restoreFocus: true,
    children: ts(
      prompt?.secret
        ? 'This secret is shown only once. Leave only after you copy it.'
        : prompt?.editor
          ? 'The workflow has unsaved changes. Leave and discard them?'
          : 'The changes in this section are not saved. Leave and discard them?'
    ),
  });

  return {
    setDirty,
    setSecretVisible,
    guard,
    setEditorDirty,
    guardLeave,
    atRisk,
    confirmDialog,
  };
}

const SettingsLeaveGuardContext = createContext<SettingsLeaveGuard | null>(
  null
);

/**
 * Shares one guard between the Workflow panel and the workspace navigation
 * (mode tabs, palette).
 */
export function SettingsLeaveGuardProvider({
  children,
}: {
  children: ReactNode;
}) {
  const controller = useGuardController();
  const {
    setDirty,
    setSecretVisible,
    guard,
    setEditorDirty,
    guardLeave,
    atRisk,
  } = controller;
  const value = useMemo<SettingsLeaveGuard>(
    () => ({
      setDirty,
      setSecretVisible,
      guard,
      setEditorDirty,
      guardLeave,
      atRisk,
      confirmDialog: null,
    }),
    [setDirty, setSecretVisible, guard, setEditorDirty, guardLeave, atRisk]
  );
  return createElement(
    SettingsLeaveGuardContext.Provider,
    { value },
    children,
    createElement(Fragment, null, controller.confirmDialog)
  );
}

/** The shared guard in a provider; else a guard local to the caller, which renders confirmDialog. */
export function useSettingsLeaveGuard(): SettingsLeaveGuard {
  const shared = useContext(SettingsLeaveGuardContext);
  const local = useGuardController();
  return shared ?? local;
}
