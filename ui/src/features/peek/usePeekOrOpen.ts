// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useContext, useEffect, useRef } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useOpenPage } from '@/features/dag-runs/lib/useOpenPage';
import { PeekContext } from './PeekContext';
import {
  isPeekTarget,
  peekPageURL,
  samePeekTarget,
  type PeekTarget,
} from './peekParams';

type ClickModifiers = { ctrlKey?: boolean; metaKey?: boolean };

/** Below this viewport width the sheet would hide the whole list. */
const PEEK_MIN_VIEWPORT = 768;

/**
 * Shows a target in the peek of the nearest PeekHost. It returns false, and
 * shows nothing, without a host, on a narrow viewport or for a target that
 * the server cannot have: the caller then opens the full page.
 */
export function useShowPeek(): (target: PeekTarget) => boolean {
  const peek = useContext(PeekContext);
  return useCallback(
    (target) => {
      if (
        !peek ||
        window.innerWidth < PEEK_MIN_VIEWPORT ||
        !isPeekTarget(target)
      ) {
        return false;
      }
      peek.open(target);
      return true;
    },
    [peek]
  );
}

/**
 * Opens a target from a row of a list: a plain click shows the peek over the
 * list, a Ctrl or Cmd click opens the full page in a new tab. Where no peek
 * can show, a plain click opens the full page.
 */
export function usePeekOrOpen(
  remoteNode?: string
): (target: PeekTarget, event?: ClickModifiers) => void {
  const showPeek = useShowPeek();
  const openPage = useOpenPage();
  const node = useRemoteNode(remoteNode);
  return useCallback(
    (target, event) => {
      const newTab = Boolean(event?.ctrlKey || event?.metaKey);
      if (!newTab && showPeek(target)) return;
      openPage(peekPageURL(target, node), event);
    },
    [node, openPage, showPeek]
  );
}

/** True while a peek is open over the list: list keys stand aside. */
export function usePeekIsOpen(): boolean {
  return useContext(PeekContext)?.isOpen ?? false;
}

/**
 * Gives the peek the targets of the rows of a list, in their order, so that
 * the arrow keys of the peek step through the rows. The list that changed
 * its rows last wins, and a list takes its rows away when it unmounts.
 */
export function usePeekSequence(targets: readonly PeekTarget[]): void {
  const setSequence = useContext(PeekContext)?.setSequence;
  const latest = useRef(targets);
  latest.current = targets;
  const key = targets
    .map((target) => `${target.dag}\n${target.run ?? ''}`)
    .join('\0');
  useEffect(() => {
    setSequence?.(latest.current);
  }, [setSequence, key]);
  useEffect(() => () => setSequence?.([]), [setSequence]);
}

/** The mark of the row that the peek shows, or showed last. */
export const PEEK_SHOWN_ROW = 'shadow-[inset_3px_0_0_0_var(--primary)]';

/**
 * Tells whether the peek shows the row of a target, or showed it last
 * before it closed. Such a row carries PEEK_SHOWN_ROW and the
 * data-peek-shown attribute, which useRevealShownRow scrolls to.
 */
export function usePeekShown(): (target: PeekTarget) => boolean {
  const shown = useContext(PeekContext)?.shown ?? null;
  return useCallback(
    (target) => shown !== null && samePeekTarget(shown, target),
    [shown]
  );
}

/**
 * Scrolls the row that the peek shows into view each time the peek moves
 * to another row, so that the arrow keys of the peek never leave it out of
 * sight. Call it once in a list whose rows carry data-peek-shown.
 */
export function useRevealShownRow(): void {
  const shown = useContext(PeekContext)?.shown ?? null;
  const key = shown ? `${shown.dag}\n${shown.run ?? ''}` : null;
  useEffect(() => {
    if (key === null) return;
    document
      .querySelector('[data-peek-shown]')
      ?.scrollIntoView?.({ block: 'nearest' });
  }, [key]);
}
