// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from 'react';
import { copyText } from '@/lib/clipboard';

/**
 * Copies text to the clipboard and exposes a transient `copied` flag for
 * button feedback. The flag resets after `resetMs` milliseconds. `onCopied`
 * runs each time the text reached the clipboard.
 */
export function useCopyFeedback(
  resetMs = 2000,
  onCopied?: () => void
): {
  copied: boolean;
  copy: (text: string) => Promise<void>;
} {
  const [copied, setCopied] = useState(false);
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onCopiedRef = useRef(onCopied);
  useEffect(() => {
    onCopiedRef.current = onCopied;
  });

  const copy = useCallback(
    async (text: string) => {
      if (!text) return;
      if (!(await copyText(text))) return;
      setCopied(true);
      if (resetRef.current) {
        clearTimeout(resetRef.current);
      }
      resetRef.current = setTimeout(() => setCopied(false), resetMs);
      onCopiedRef.current?.();
    },
    [resetMs]
  );

  useEffect(
    () => () => {
      if (resetRef.current) {
        clearTimeout(resetRef.current);
      }
    },
    []
  );

  return { copied, copy };
}
