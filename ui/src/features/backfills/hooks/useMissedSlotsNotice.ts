// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { MissedSlotsReason } from '@/api/v1/schema';
import type { WorkspaceNotice } from '@/features/dags/components/dag-editor/hostTypes';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';

/** The dismissed missed slots of each DAG on each node. */
export const DISMISSED_GAPS_KEY = 'dagu.backfills.dismissedGaps';
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

const MESSAGES: Record<MissedSlotsReason, string> = {
  [MissedSlotsReason.suspended]:
    '{count} slots passed from {from} to {to} while this DAG was suspended',
  [MissedSlotsReason.paused]:
    '{count} slots passed from {from} to {to} while the scheduler was paused',
  [MissedSlotsReason.down]:
    '{count} slots passed from {from} to {to} while the scheduler was down',
};
const MIXED_MESSAGE =
  '{count} slots passed from {from} to {to} while this DAG could not run';

const SINGLE_SLOT_MESSAGES: Record<MissedSlotsReason, string> = {
  [MissedSlotsReason.suspended]:
    '1 slot passed at {at} while this DAG was suspended',
  [MissedSlotsReason.paused]:
    '1 slot passed at {at} while the scheduler was paused',
  [MissedSlotsReason.down]:
    '1 slot passed at {at} while the scheduler was down',
};
const SINGLE_SLOT_MIXED_MESSAGE =
  '1 slot passed at {at} while this DAG could not run';

/** The most slots the server looks at. */
const MAX_MISSED_SLOTS = 10000;

/** The dismissed `until` of each DAG, by `node/DAG`. */
type Dismissed = Record<string, string>;

const dagKey = (dagName: string, remoteNode: string) =>
  `${remoteNode}/${dagName}`;

function readDismissed(): Dismissed {
  try {
    const parsed: unknown = JSON.parse(
      localStorage.getItem(DISMISSED_GAPS_KEY) ?? '{}'
    );
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {};
    }
    return Object.fromEntries(
      Object.entries(parsed).filter(
        (entry): entry is [string, string] => typeof entry[1] === 'string'
      )
    );
  } catch {
    return {};
  }
}

function writeDismissed(dismissed: Dismissed): void {
  try {
    localStorage.setItem(DISMISSED_GAPS_KEY, JSON.stringify(dismissed));
  } catch {
    // The notice then shows again on the next visit.
  }
}

const formatBound = (value: string) =>
  formatAbsoluteTime(value, { seconds: false }) ?? value;

/**
 * The notice of the slots a DAG missed while it was suspended, the
 * scheduler paused, or no scheduler ran. For a user who may backfill, its
 * action opens the Backfills tab with the start panel on those slots. The
 * slots are read as the page opens and again when a backfill starts, never
 * polled. Dismissed, the notice stays hidden until a newer span adds missed
 * slots.
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
    { revalidateOnFocus: false, revalidateOnReconnect: false }
  );
  const missed = data?.missedSlots;
  const [dismissed, setDismissed] = useState(readDismissed);
  const dismiss = useCallback(() => {
    if (!missed) return;
    const next = {
      ...readDismissed(),
      [dagKey(dagName, remoteNode)]: missed.until,
    };
    writeDismissed(next);
    setDismissed(next);
  }, [missed, dagName, remoteNode]);

  return useMemo(() => {
    if (
      !missed ||
      missed.slots === 0 ||
      dismissed[dagKey(dagName, remoteNode)] === missed.until
    ) {
      return null;
    }
    const reason = missed.reasons.length === 1 ? missed.reasons[0] : undefined;
    const message =
      missed.slots === 1
        ? ts(
            reason ? SINGLE_SLOT_MESSAGES[reason] : SINGLE_SLOT_MIXED_MESSAGE,
            {
              at: formatBound(missed.first),
            }
          )
        : ts(reason ? MESSAGES[reason] : MIXED_MESSAGE, {
            // At the cap, more slots may have been missed.
            count:
              missed.slots >= MAX_MISSED_SLOTS
                ? `${MAX_MISSED_SLOTS}+`
                : String(missed.slots),
            from: formatBound(missed.first),
            to: formatBound(missed.last),
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
      dismiss,
      priority: MISSED_SLOTS_NOTICE_PRIORITY,
    };
  }, [
    missed,
    dagName,
    remoteNode,
    canBackfill,
    dismissed,
    dismiss,
    navigate,
    location,
    ts,
  ]);
}
