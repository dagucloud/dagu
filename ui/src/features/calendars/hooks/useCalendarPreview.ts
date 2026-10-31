// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { useClient } from '@/hooks/api';
import { useCallback, useEffect, useRef, useState } from 'react';

export type CalendarPreview = components['schemas']['CalendarPreview'];

/** Wait after the last keystroke before previewing edited content. */
export const CALENDAR_PREVIEW_DEBOUNCE_MS = 600;

type Options = {
  name: string;
  remoteNode: string;
  /** Content to preview; nothing is requested while it is null. */
  spec: string | null;
  /** The saved content that changes are marked against. */
  baseline: string | null;
  year: number;
};

/**
 * Checks calendar content and applies it to the dates of a year. Edits are
 * previewed after a pause in typing; the first preview, a change of year,
 * and a new saved baseline are requested at once. The last preview stays
 * while the next one loads, and failed reports a request that failed.
 */
export function useCalendarPreview({
  name,
  remoteNode,
  spec,
  baseline,
  year,
}: Options): {
  preview: CalendarPreview | null;
  isPreviewing: boolean;
  failed: boolean;
  retry: () => void;
} {
  const client = useClient();
  const [preview, setPreview] = useState<CalendarPreview | null>(null);
  const [isPreviewing, setIsPreviewing] = useState(false);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const requestSeqRef = useRef(0);
  const previewedSpecRef = useRef<string | null>(null);

  useEffect(() => {
    setPreview(null);
    previewedSpecRef.current = null;
  }, [name, remoteNode]);

  useEffect(() => {
    if (spec === null) {
      return;
    }
    const seq = ++requestSeqRef.current;
    const edited =
      previewedSpecRef.current !== null && previewedSpecRef.current !== spec;
    setIsPreviewing(true);
    const timer = window.setTimeout(
      async () => {
        let data: CalendarPreview | undefined;
        try {
          ({ data } = await client.POST('/calendars/{name}/preview', {
            params: { path: { name }, query: { remoteNode } },
            body: { spec, year },
          }));
        } catch {
          data = undefined;
        }
        if (seq !== requestSeqRef.current) {
          return;
        }
        if (data) {
          previewedSpecRef.current = spec;
          setPreview(data);
        }
        setFailed(!data);
        setIsPreviewing(false);
      },
      edited ? CALENDAR_PREVIEW_DEBOUNCE_MS : 0
    );
    return () => window.clearTimeout(timer);
  }, [client, name, remoteNode, spec, baseline, year, attempt]);

  const retry = useCallback(() => setAttempt((count) => count + 1), []);
  return { preview, isPreviewing, failed, retry };
}
