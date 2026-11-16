// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { MissedSlotsReason } from '@/api/v1/schema';
import type { WorkspaceNotice } from '@/features/dags/components/dag-editor/hostTypes';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';

const MISSED_SLOTS_NOTICE_PRIORITY = 15;
const MISSED_SLOTS_PATH = '/dags/{fileName}/missed-slots';

/**
 * Whether an SWR key is a query of a DAG's missed slots, which a started
 * backfill may fill.
 */
export const isMissedSlotsKey = (key: unknown): boolean =>
  Array.isArray(key) && key[1] === MISSED_SLOTS_PATH;

/** The query that opens the Backfills tab on a range to backfill. */
export const GAP_FROM_PARAM = 'gapFrom';
export const GAP_TO_PARAM = 'gapTo';

/** Why the slots did not run; slots missed for several reasons name none. */
const WHY: Record<MissedSlotsReason | 'several', string> = {
  [MissedSlotsReason.suspended]: 'this DAG was suspended',
  [MissedSlotsReason.paused]: 'the scheduler was paused',
  [MissedSlotsReason.down]: 'the scheduler was down',
  several: 'this DAG could not run',
};

/** The most slots the server looks at. */
const MAX_MISSED_SLOTS = 10000;

/** Where the browser keeps the dismissed missed slots of a DAG on a node. */
const dismissedKey = (remoteNode: string, dagName: string) =>
  `dagu.backfills.dismissedMissedSlots/${remoteNode}/${dagName}`;

function readDismissed(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

const formatBound = (value: string) =>
  formatAbsoluteTime(value, { seconds: false }) ?? value;

/**
 * The notice of the slots a DAG missed while it was suspended, the
 * scheduler paused, or no scheduler ran. For a user who may backfill, its
 * action opens the Backfills tab with the start panel on those slots. The
 * slots are read as the page opens and again when a backfill starts, never
 * polled or retried. Dismissed, the notice stays hidden until a newer span
 * adds missed slots, which moves their `until` later.
 */
export function useMissedSlotsNotice(
  fileName: string,
  dagName: string,
  remoteNode: string,
  canBackfill: boolean
): WorkspaceNotice | null {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const location = useLocation();
  const { data } = useQuery(
    MISSED_SLOTS_PATH,
    fileName ? { params: { path: { fileName }, query: { remoteNode } } } : null,
    // A failed read may be a slow first import of the DAG's slot history,
    // which a retry would start over.
    {
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      shouldRetryOnError: false,
    }
  );
  const missed = data?.missedSlots;
  const key = dismissedKey(remoteNode, dagName);
  // The last dismissal of this visit, which holds where the browser keeps
  // none.
  const [dismissed, setDismissed] = useState<{ key: string; until: string }>();

  return useMemo(() => {
    const dismissedUntil =
      (dismissed?.key === key ? dismissed.until : null) ?? readDismissed(key);
    if (
      !missed ||
      missed.slots === 0 ||
      (dismissedUntil !== null &&
        Date.parse(missed.until) <= Date.parse(dismissedUntil))
    ) {
      return null;
    }
    const [reason] = missed.reasons;
    const why = ts(
      WHY[missed.reasons.length === 1 && reason ? reason : 'several']
    );
    const message =
      missed.slots === 1
        ? ts('1 slot passed at {at} while {why}', {
            at: formatBound(missed.first),
            why,
          })
        : ts('{count} slots passed from {from} to {to} while {why}', {
            // At the cap, more slots may have been missed.
            count:
              missed.slots >= MAX_MISSED_SLOTS
                ? `${MAX_MISSED_SLOTS}+`
                : String(missed.slots),
            from: formatBound(missed.first),
            to: formatBound(missed.last),
            why,
          });
    const action = {
      label: ts('Backfill'),
      run: () => {
        const search = new URLSearchParams(location.search);
        search.set('panel', 'workflow');
        search.set('wtab', 'backfills');
        search.set(GAP_FROM_PARAM, missed.first);
        search.set(GAP_TO_PARAM, missed.last);
        navigate({ pathname: location.pathname, search: `?${search}` });
      },
    };
    return {
      id: 'missed-slots',
      tone: 'warning',
      message,
      action: canBackfill ? action : undefined,
      dismissible: true,
      dismiss: () => {
        try {
          localStorage.setItem(key, missed.until);
        } catch {
          // The notice then shows again on the next visit.
        }
        setDismissed({ key, until: missed.until });
      },
      priority: MISSED_SLOTS_NOTICE_PRIORITY,
    };
  }, [missed, key, dismissed, canBackfill, navigate, location, ts]);
}
