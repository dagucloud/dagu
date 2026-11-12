// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { FlaskConical } from 'lucide-react';
import { useMemo } from 'react';
import { isEphemeralRun } from '@/features/visual-editor/run/inputProvenance';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import type { ApiDAGRunDetails, WorkspaceNotice } from '../contracts';
import { TEST_RUN_TTL_MINUTES, testRunExpiry } from '../data/testRun';

/** The priority of the notice of a test run: under the waiting actions, over the offers. */
export const TEST_RUN_NOTICE_PRIORITY = 20;

/**
 * The notice of the canvas while the shown run is a test run: it is not
 * kept, and it disappears ten minutes after it ends. null for any other run.
 */
export function useTestRunNotice(
  details: ApiDAGRunDetails | null | undefined
): WorkspaceNotice | null {
  const { ts } = useI18n();
  const ephemeral = isEphemeralRun(details);
  const expiry = testRunExpiry(details);
  return useMemo(() => {
    if (!ephemeral) return null;
    const text =
      expiry === null
        ? ts(
            'Test run, not kept: it disappears {minutes} minutes after it ends',
            {
              minutes: TEST_RUN_TTL_MINUTES,
            }
          )
        : ts('Test run, not kept: it disappears at {time}', {
            time: dayjs(expiry).format('HH:mm:ss'),
          });
    return {
      id: 'test-run',
      tone: 'info',
      message: (
        <span className="inline-flex items-center gap-1.5">
          <FlaskConical aria-hidden="true" className="size-3.5 shrink-0" />
          {text}
        </span>
      ),
      priority: TEST_RUN_NOTICE_PRIORITY,
    };
  }, [ephemeral, expiry, ts]);
}
