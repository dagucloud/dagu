// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { MissedSlotsReason, type components } from '@/api/v1/schema';
import type { WorkspaceNotice } from '@/features/dags/components/dag-editor/hostTypes';
import { useI18n } from '@/i18n/I18nProvider';

type MissedSlots = components['schemas']['MissedSlots'];

/** The gaps whose notice was dismissed, newest last. */
export const DISMISSED_GAPS_KEY = 'dagu.backfills.dismissedGaps';
const DISMISSED_KEEP = 20;
const MISSED_SLOTS_NOTICE_PRIORITY = 15;

/** The query that opens the Backfills tab on a gap to backfill. */
export const GAP_FROM_PARAM = 'gapFrom';
export const GAP_TO_PARAM = 'gapTo';

const MESSAGES: Record<MissedSlotsReason, string> = {
  [MissedSlotsReason.suspended]:
    '{count} slots passed while this DAG was suspended',
  [MissedSlotsReason.paused]:
    '{count} slots passed while the scheduler was paused',
  [MissedSlotsReason.down]: '{count} slots passed while the scheduler was down',
};

/** The most slots the scheduler counts in a gap. */
const MAX_GAP_SLOTS = 10000;

/** A gap's dismissal holds for its DAG on its node only. */
const gapKey = (gap: MissedSlots, dagName: string, remoteNode: string) =>
  `${remoteNode}/${dagName}/${gap.from}/${gap.to}`;

function readDismissed(): string[] {
  try {
    const parsed: unknown = JSON.parse(
      localStorage.getItem(DISMISSED_GAPS_KEY) ?? '[]'
    );
    return Array.isArray(parsed)
      ? parsed.filter((key): key is string => typeof key === 'string')
      : [];
  } catch {
    return [];
  }
}

function writeDismissed(keys: string[]): void {
  try {
    localStorage.setItem(
      DISMISSED_GAPS_KEY,
      JSON.stringify(keys.slice(-DISMISSED_KEEP))
    );
  } catch {
    // The notice then shows again on the next visit.
  }
}

/**
 * The notice of the slots a DAG missed while it was suspended, the
 * scheduler paused, or no scheduler ran. Its action opens the Backfills tab
 * with the start panel on that gap. Dismissed, it stays hidden until
 * another gap is missed.
 */
export function useMissedSlotsNotice(
  gap: MissedSlots | undefined,
  dagName: string,
  remoteNode: string
): WorkspaceNotice | null {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const location = useLocation();
  const [dismissed, setDismissed] = useState(readDismissed);
  const dismiss = useCallback(() => {
    if (!gap) return;
    const next = [...readDismissed(), gapKey(gap, dagName, remoteNode)];
    writeDismissed(next);
    setDismissed(next);
  }, [gap, dagName, remoteNode]);

  return useMemo(() => {
    if (
      !gap ||
      gap.slots === 0 ||
      dismissed.includes(gapKey(gap, dagName, remoteNode))
    ) {
      return null;
    }
    // A gap at the cap may hold more slots than it counts.
    const count =
      gap.slots >= MAX_GAP_SLOTS ? `${MAX_GAP_SLOTS}+` : String(gap.slots);
    return {
      id: 'missed-slots',
      tone: 'warning',
      message: ts(MESSAGES[gap.reason], { count }),
      action: {
        label: ts('Backfill'),
        run: () => {
          const search = new URLSearchParams(location.search);
          search.set('panel', 'workflow');
          search.set('wtab', 'backfills');
          search.set(GAP_FROM_PARAM, gap.from);
          search.set(GAP_TO_PARAM, gap.to);
          navigate({ pathname: location.pathname, search: `?${search}` });
        },
      },
      dismissible: true,
      dismiss,
      priority: MISSED_SLOTS_NOTICE_PRIORITY,
    };
  }, [gap, dagName, remoteNode, dismissed, dismiss, navigate, location, ts]);
}
