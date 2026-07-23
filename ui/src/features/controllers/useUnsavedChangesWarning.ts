// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';

export function useUnsavedChangesWarning(
  enabled: boolean,
  message: string,
  navigationBlocked = false
) {
  React.useEffect(() => {
    if (!enabled && !navigationBlocked) return;

    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    const guardLinkNavigation = (event: MouseEvent) => {
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey ||
        !(event.target instanceof Element)
      ) {
        return;
      }
      const anchor = event.target.closest<HTMLAnchorElement>('a[href]');
      if (
        !anchor ||
        anchor.hasAttribute('download') ||
        (anchor.target && anchor.target !== '_self')
      ) {
        return;
      }
      const next = new URL(anchor.href, window.location.href);
      if (
        next.origin !== window.location.origin ||
        next.href === window.location.href ||
        (!navigationBlocked && window.confirm(message))
      ) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
    };

    const currentIndex = window.history.state?.idx;
    let restoringHistory = false;
    const guardHistoryNavigation = (event: PopStateEvent) => {
      if (restoringHistory) {
        restoringHistory = false;
        event.stopImmediatePropagation();
        return;
      }
      const nextIndex = event.state?.idx;
      if (
        typeof currentIndex !== 'number' ||
        typeof nextIndex !== 'number' ||
        currentIndex === nextIndex ||
        (!navigationBlocked && window.confirm(message))
      ) {
        return;
      }
      event.stopImmediatePropagation();
      restoringHistory = true;
      window.history.go(currentIndex - nextIndex);
    };

    window.addEventListener('beforeunload', warnBeforeUnload);
    document.addEventListener('click', guardLinkNavigation, true);
    window.addEventListener('popstate', guardHistoryNavigation, true);
    return () => {
      window.removeEventListener('beforeunload', warnBeforeUnload);
      document.removeEventListener('click', guardLinkNavigation, true);
      window.removeEventListener('popstate', guardHistoryNavigation, true);
    };
  }, [enabled, message, navigationBlocked]);
}
