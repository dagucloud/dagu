// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
import { useHref, useLocation, useParams } from 'react-router-dom';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import { useSettingsLeaveGuard } from './useSettingsLeaveGuard';

function insideBase(pathname: string, base: string): boolean {
  return pathname === base || pathname.startsWith(`${base}/`);
}

/**
 * Asks before the editor draft or a draft of the Workflow panel is lost. The
 * router has no blocker (the app uses BrowserRouter), so three exits are
 * covered by hand: unload (reload, close, external link), a click on a link
 * that leaves the workflow, and browser Back or Forward. The run page entry
 * calls guardLeave itself. Renders nothing.
 */
export function EditorLeaveGuard() {
  const { state } = useWorkspaceEditor();
  const { setEditorDirty, guardLeave, atRisk } = useSettingsLeaveGuard();
  const { pathname, key } = useLocation();
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

  // The history index of the entry that the router shows. Navigation of the
  // router stores it in history.state.
  const index = useRef<number | null>(null);
  useEffect(() => {
    const value = (window.history.state as { idx?: unknown } | null)?.idx;
    index.current = typeof value === 'number' ? value : null;
  }, [key]);

  useEffect(() => {
    if (!dirty && !atRisk) return;
    // Set while a confirmed exit replays the event that was held back.
    let replaying = false;

    const onClick = (event: MouseEvent) => {
      if (replaying || event.defaultPrevented || event.button !== 0) return;
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
        return;
      }
      const target = event.target instanceof Element ? event.target : null;
      const anchor = target?.closest('a[href]');
      if (!(anchor instanceof HTMLAnchorElement)) return;
      if (anchor.target && anchor.target !== '_self') return;
      if (anchor.hasAttribute('download')) return;
      const url = new URL(anchor.href, window.location.href);
      if (url.origin !== window.location.origin) return;
      if (insideBase(url.pathname.replace(/\/+$/, ''), baseHref)) return;
      event.preventDefault();
      event.stopPropagation();
      guardLeave(() => {
        replaying = true;
        try {
          anchor.click();
        } finally {
          replaying = false;
        }
      });
    };

    const onPopState = (event: PopStateEvent) => {
      if (replaying) return;
      const from = index.current;
      const to = (window.history.state as { idx?: unknown } | null)?.idx;
      if (typeof from !== 'number' || typeof to !== 'number' || from === to) {
        return;
      }
      if (insideBase(window.location.pathname.replace(/\/+$/, ''), baseHref)) {
        return;
      }
      // Keep the router on the workflow, then ask.
      event.stopImmediatePropagation();
      window.history.go(from - to);
      guardLeave(() => {
        replaying = true;
        window.history.go(to - from);
      });
    };

    document.addEventListener('click', onClick, true);
    window.addEventListener('popstate', onPopState, true);
    return () => {
      document.removeEventListener('click', onClick, true);
      window.removeEventListener('popstate', onPopState, true);
    };
  }, [dirty, atRisk, baseHref, guardLeave]);

  return null;
}
