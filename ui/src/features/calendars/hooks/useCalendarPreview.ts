// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import { useContext, useEffect, useRef, useState } from 'react';

export type CalendarPreview = components['schemas']['CalendarPreview'];

/** Wait after the last keystroke before previewing edited content. */
export const CALENDAR_PREVIEW_DEBOUNCE_MS = 600;

/**
 * Checks calendar content and applies it to the dates of year. Edits are
 * previewed after a pause in typing; the first preview and a change of year
 * are requested at once. The last preview stays shown while the next one
 * loads or when a request fails.
 */
export function useCalendarPreview(
  name: string,
  spec: string | null,
  year: number
): { preview: CalendarPreview | null; isPreviewing: boolean } {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const [preview, setPreview] = useState<CalendarPreview | null>(null);
  const [isPreviewing, setIsPreviewing] = useState(false);
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
        previewedSpecRef.current = spec;
        if (data) {
          setPreview(data);
        }
        setIsPreviewing(false);
      },
      edited ? CALENDAR_PREVIEW_DEBOUNCE_MS : 0
    );
    return () => window.clearTimeout(timer);
  }, [client, name, remoteNode, spec, year]);

  return { preview, isPreviewing };
}
