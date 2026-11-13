// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect } from 'react';
import { useHref, useLocation, useParams } from 'react-router-dom';
import { useLeaveGuardHost } from '@/contexts/LeaveGuardHostContext';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import { useLeaveGuardLinks } from './useLeaveGuardLinks';
import { useSettingsLeaveGuard } from './useSettingsLeaveGuard';

/**
 * Asks before the editor draft or a draft of the Workflow panel is lost.
 * Unload (reload, close, external link) is covered by the guard; a click on a
 * link that leaves the workflow and browser Back or Forward by
 * useLeaveGuardLinks. The run page entry calls guardLeave itself, and a host
 * such as the workflow overlay asks before it closes. Renders nothing.
 */
export function EditorLeaveGuard() {
  const { state } = useWorkspaceEditor();
  const { setEditorDirty, guardLeave, leaveAtRisk, atRisk } =
    useSettingsLeaveGuard();
  const register = useLeaveGuardHost()?.register;
  const { pathname } = useLocation();
  const { tab } = useParams<{ tab?: string }>();
  const trimmed = pathname.replace(/\/+$/, '');
  const base = tab
    ? trimmed.slice(0, trimmed.length - tab.length - 1)
    : trimmed;
  const baseHref = useHref(base).replace(/\/+$/, '');
  const dirty = !!state?.dirty;

  useEffect(() => {
    setEditorDirty(dirty);
    return () => setEditorDirty(false);
  }, [dirty, setEditorDirty]);

  useEffect(() => register?.(guardLeave), [register, guardLeave]);
  useLeaveGuardLinks({
    dirty,
    atRisk,
    leaveAtRisk,
    baseHref,
    guard: guardLeave,
  });

  return null;
}
