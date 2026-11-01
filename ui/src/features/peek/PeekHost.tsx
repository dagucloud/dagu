// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useMemo,
  useRef,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
} from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import type { components } from '@/api/v1/schema';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useOpenPage } from '@/features/dag-runs/lib/useOpenPage';
import { PeekContext, type PeekController } from './PeekContext';
import {
  peekPageURL,
  readPeek,
  withoutPeek,
  withPeek,
  type PeekTarget,
} from './peekParams';
import { WorkflowPeek } from './WorkflowPeek';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

/** The router state of the history entry that opening a peek pushes. */
const PEEK_STATE = { peek: true } as const;

function isPeekEntry(state: unknown): boolean {
  return (
    typeof state === 'object' &&
    state !== null &&
    (state as { peek?: unknown }).peek === true
  );
}

// Enter on these clicks or types; the full page does not take it.
const OWN_KEYS_SELECTOR = [
  'a[href]',
  'button',
  'input',
  'select',
  'textarea',
  '[contenteditable=""]',
  '[contenteditable="true"]',
  '[role="button"]',
  '[role="checkbox"]',
  '[role="combobox"]',
  '[role="menuitem"]',
  '[role="option"]',
  '[role="switch"]',
  '[role="tab"]',
  '[role="textbox"]',
].join(',');

// A right sheet over a dimmed list: the dialog in its fullscreen form, with
// the left quarter of the window left to the backdrop.
const SHEET_CLASS =
  'left-auto flex w-full flex-col border-0 border-l bg-card outline-none md:w-3/4 data-[state=open]:slide-in-from-right data-[state=closed]:slide-out-to-right';

/**
 * Shows a peek over the list below it: a sheet with what the ?peek target of
 * the URL is. Opening pushes a history entry, so Back closes the peek, as Esc,
 * the backdrop and the close button do. Enter or F opens the full page, and
 * Ctrl or Cmd with Enter opens it in a new tab.
 */
export function PeekHost({ children }: { children: ReactNode }): ReactElement {
  const location = useLocation();
  const navigate = useNavigate();
  const remoteNode = useRemoteNode();
  const openPage = useOpenPage();
  const target = useMemo(
    () => readPeek(new URLSearchParams(location.search)),
    [location.search]
  );
  // The last target stays on show while the sheet slides out.
  const lastTarget = useRef<PeekTarget | null>(null);
  if (target) lastTarget.current = target;
  const shown = target ?? lastTarget.current;
  const locationRef = useRef(location);
  locationRef.current = location;
  // The entry that a close left, until the location moves.
  const closedFrom = useRef<string | null>(null);
  if (closedFrom.current !== location.key) closedFrom.current = null;
  const contentRef = useRef<HTMLDivElement>(null);

  const open = useCallback(
    (next: PeekTarget) => {
      const { pathname, search } = locationRef.current;
      navigate(
        { pathname, search: withPeek(search, next) },
        { state: PEEK_STATE }
      );
    },
    [navigate]
  );

  const close = useCallback(() => {
    const { key, pathname, search, state } = locationRef.current;
    // A second close before the first one lands would leave the list.
    if (closedFrom.current === key) return;
    closedFrom.current = key;
    // A peek that a link or a reload opened has no entry of its own.
    if (isPeekEntry(state)) {
      navigate(-1);
    } else {
      navigate({ pathname, search: withoutPeek(search) }, { replace: true });
    }
  }, [navigate]);

  const selectRun = useCallback(
    (run: DAGRunDetails) => {
      const { pathname, search, state } = locationRef.current;
      navigate(
        {
          pathname,
          search: withPeek(search, { dag: run.name, run: run.dagRunId }),
        },
        { replace: true, state }
      );
    },
    [navigate]
  );

  const isOpen = target !== null;
  const controller = useMemo<PeekController>(
    () => ({ open, isOpen }),
    [open, isOpen]
  );

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const element = event.target as Element;
    // The keys of a dialog that the peek opened reach it through the React
    // tree, not through its DOM.
    if (
      !shown ||
      event.defaultPrevented ||
      event.altKey ||
      !event.currentTarget.contains(element) ||
      element.closest(OWN_KEYS_SELECTOR)
    ) {
      return;
    }
    const newTab = event.ctrlKey || event.metaKey;
    if (event.key === 'Enter' || (!newTab && event.key.toLowerCase() === 'f')) {
      event.preventDefault();
      openPage(peekPageURL(shown, remoteNode), { ctrlKey: newTab });
    }
  };

  return (
    <PeekContext.Provider value={controller}>
      {children}
      {shown && (
        <Dialog
          open={isOpen}
          onOpenChange={(next) => {
            if (!next) close();
          }}
        >
          <DialogContent
            ref={contentRef}
            fullscreen
            hideCloseButton
            aria-describedby={undefined}
            className={SHEET_CLASS}
            // The sheet itself takes the focus, so that Enter opens the page.
            onOpenAutoFocus={(event) => {
              event.preventDefault();
              contentRef.current?.focus();
            }}
            onKeyDown={onKeyDown}
          >
            <WorkflowPeek
              target={shown}
              remoteNode={remoteNode}
              onClose={close}
              onSelectRun={selectRun}
            />
          </DialogContent>
        </Dialog>
      )}
    </PeekContext.Provider>
  );
}
