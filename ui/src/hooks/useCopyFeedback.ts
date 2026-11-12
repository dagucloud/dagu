// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from 'react';
import { copyText } from '@/lib/clipboard';

/**
 * Copies text to the clipboard and exposes a transient `copied` flag for
 * button feedback. The flag resets after `resetMs` milliseconds, or at once on
 * `reset`, and no reset is left pending after unmount. `onCopied` runs each
 * time a copy completes. A copy still in progress at `reset` or unmount shows
 * no feedback and does not call `onCopied`, so callers never update state for
 * content that is gone.
 */
export function useCopyFeedback(
  resetMs = 2000,
  onCopied?: () => void
): {
  copied: boolean;
  copy: (text: string) => Promise<void>;
  reset: () => void;
} {
  const [copied, setCopied] = useState(false);
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Advanced by reset and unmount, so a copy that finishes later is ignored.
  const generationRef = useRef(0);
  const onCopiedRef = useRef(onCopied);
  useEffect(() => {
    onCopiedRef.current = onCopied;
  });

  const copy = useCallback(
    async (text: string) => {
      if (!text) return;
      const generation = generationRef.current;
      if (!(await copyText(text))) return;
      if (generation !== generationRef.current) return;
      setCopied(true);
      if (resetRef.current) {
        clearTimeout(resetRef.current);
      }
      resetRef.current = setTimeout(() => setCopied(false), resetMs);
      onCopiedRef.current?.();
    },
    [resetMs]
  );

  const reset = useCallback(() => {
    generationRef.current += 1;
    if (resetRef.current) {
      clearTimeout(resetRef.current);
      resetRef.current = null;
    }
    setCopied(false);
  }, []);

  useEffect(
    () => () => {
      generationRef.current += 1;
      if (resetRef.current) {
        clearTimeout(resetRef.current);
      }
    },
    []
  );

  return { copied, copy, reset };
}
