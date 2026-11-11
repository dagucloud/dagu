// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef } from 'react';
import { useLocation } from 'react-router-dom';

function insideBase(pathname: string, base: string): boolean {
  return pathname === base || pathname.startsWith(`${base}/`);
}

/**
 * Asks before a draft is lost. The router has no blocker (the app uses
 * BrowserRouter), so two exits are covered by hand while `dirty` is true: a
 * click on a link that leaves `baseHref`, and browser Back or Forward. A move
 * inside `baseHref` is free. `guard` runs the move at once or after the user
 * confirms; see useSettingsLeaveGuard. The unload prompt is the guard's.
 * `atRisk` also arms the hook, for a draft that is not `dirty`. A Back or
 * Forward move that `leaveAtRisk` finds safe, such as the close of the
 * workflow overlay, has nothing to ask about.
 */
export function useLeaveGuardLinks({
  dirty,
  atRisk = false,
  leaveAtRisk,
  baseHref,
  guard,
}: {
  dirty: boolean;
  /** A draft other than the dirty one is at risk. */
  atRisk?: boolean;
  /** True when a move out of the page would lose a draft now. */
  leaveAtRisk?: () => boolean;
  /** The href of the page that holds the draft, without a trailing slash. */
  baseHref: string;
  guard: (next: () => void) => void;
}): void {
  const { key } = useLocation();

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
      guard(() => {
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
      if (leaveAtRisk && !leaveAtRisk()) return;
      // Keep the router on the page, then ask.
      event.stopImmediatePropagation();
      window.history.go(from - to);
      guard(() => {
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
  }, [dirty, atRisk, baseHref, guard, leaveAtRisk]);
}
