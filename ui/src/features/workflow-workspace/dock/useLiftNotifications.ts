// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, type RefObject } from 'react';
import { setNotificationBottomInset } from '@/components/ui/notification-root';

/**
 * Keeps the notifications above the panel while `active`: the toasts and the
 * run progress cards then cover no part of it. The room follows the height
 * of the panel and the size of the window.
 */
export function useLiftNotifications(
  ref: RefObject<HTMLElement | null>,
  active: boolean
): void {
  useEffect(() => {
    const element = ref.current;
    if (!active || !element) return;
    const update = () =>
      setNotificationBottomInset(
        Math.max(0, window.innerHeight - element.getBoundingClientRect().top)
      );
    update();
    const observer = new ResizeObserver(update);
    observer.observe(element);
    window.addEventListener('resize', update);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', update);
      setNotificationBottomInset(0);
    };
  }, [ref, active]);
}
