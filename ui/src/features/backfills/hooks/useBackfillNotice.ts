// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useState } from 'react';
import { useHref, useNavigate } from 'react-router-dom';
import { BackfillSlotState, BackfillStatus } from '@/api/v1/schema';
import type { WorkspaceNotice } from '@/features/dags/components/dag-editor/hostTypes';
import { useI18n } from '@/i18n/I18nProvider';
import {
  type Backfill,
  isOpenBackfill,
  slotsDone,
  slotsIn,
} from '../lib/backfillState';
import { backfillPath } from '../lib/backfillLinks';
import { useLiveBackfill } from './useBackfill';

/** The backfills whose failure notice was dismissed, newest last. */
export const DISMISSED_BACKFILLS_KEY = 'dagu.backfills.dismissedNotices';
const DISMISSED_KEEP = 20;

function readDismissed(): string[] {
  try {
    const parsed: unknown = JSON.parse(
      localStorage.getItem(DISMISSED_BACKFILLS_KEY) ?? '[]'
    );
    return Array.isArray(parsed)
      ? parsed.filter((id): id is string => typeof id === 'string')
      : [];
  } catch {
    return [];
  }
}

function writeDismissed(ids: string[]): void {
  try {
    localStorage.setItem(
      DISMISSED_BACKFILLS_KEY,
      JSON.stringify(ids.slice(-DISMISSED_KEEP))
    );
  } catch {
    // The notice then shows again on the next visit.
  }
}

/**
 * The notice of a DAG's latest backfill: one that runs, with its progress,
 * or one that ended failed while a slot it failed has not succeeded since.
 * An open backfill follows its live feed while the tab is visible. A
 * failure notice can be dismissed; it stays dismissed for that backfill.
 */
export function useBackfillNotice(
  latest: Backfill | undefined,
  remoteNode: string
): WorkspaceNotice | null {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const backfill = useLiveBackfill(latest, remoteNode);
  const path = backfill ? backfillPath(backfill.id, remoteNode) : '/';
  // A new tab opens the page under the app's base path.
  const href = useHref(path);
  const [dismissed, setDismissed] = useState(readDismissed);
  const dismiss = useCallback(() => {
    if (!backfill) return;
    const next = [...readDismissed(), backfill.id];
    writeDismissed(next);
    setDismissed(next);
  }, [backfill]);

  return useMemo(() => {
    if (!backfill) return null;
    const action = {
      label: ts('Open backfill'),
      run: (click?: { metaKey: boolean; ctrlKey: boolean }) =>
        click?.metaKey || click?.ctrlKey
          ? window.open(href, '_blank', 'noopener')
          : navigate(path),
    };
    if (isOpenBackfill(backfill)) {
      const failed = slotsIn(backfill, BackfillSlotState.failed);
      const values = {
        done: slotsDone(backfill),
        total: backfill.slotCount,
        failed,
      };
      return {
        id: 'backfill',
        tone: 'info',
        message:
          failed > 0
            ? ts(
                'Backfill running: {done} of {total} slots done, {failed} failed',
                values
              )
            : ts('Backfill running: {done} of {total} slots done', values),
        action,
        priority: BACKFILL_NOTICE_PRIORITY,
      };
    }
    if (
      backfill.status !== BackfillStatus.failed ||
      dismissed.includes(backfill.id)
    ) {
      return null;
    }
    return {
      id: 'backfill',
      tone: 'warning',
      message: ts('Backfill failed: {failed} slots did not succeed', {
        failed:
          slotsIn(backfill, BackfillSlotState.failed) +
          slotsIn(backfill, BackfillSlotState.not_run),
      }),
      action,
      dismissible: true,
      dismiss,
      priority: BACKFILL_NOTICE_PRIORITY,
    };
  }, [backfill, dismissed, dismiss, navigate, path, href, ts]);
}

/** Below the notices of the selected run, above the monitoring offer. */
const BACKFILL_NOTICE_PRIORITY = 20;
