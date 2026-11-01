// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, useState } from 'react';
import { isTextEntryTarget } from '@/features/visual-editor/canvas/useCanvasKeymap';
import { useWorkspaceRefresh, useWorkspaceRun } from '../data';
import { useVisibleRuns } from '../data/useVisibleRuns';
import { useDockCommands } from '../dock/DockContext';
import { useWorkspaceUrl } from '../state/workspaceUrl';

/** Page keys of the workspace. */
export type WorkspaceKeyAction =
  | 'older'
  | 'newer'
  | 'latest'
  | 'refresh'
  | 'stepLog';

// Dialogs, the dock panels (artifacts tree, log viewers), open menus and
// text boxes keep their keys.
const KEY_OWNER_SELECTOR = [
  '[role="dialog"]',
  '[role="alertdialog"]',
  '[role="tabpanel"]',
  '[role="tree"]',
  '[role="menu"]',
  '[role="listbox"]',
  '[role="textbox"]',
].join(', ');
const OPEN_MODAL_SELECTOR = '[aria-modal="true"]';
// An inactive pane stays mounted, hidden and inert, with its
// dialogs: nothing in it owns a key.
const INACTIVE_SELECTOR = '[inert], [hidden]';

function inInactivePane(node: Element): boolean {
  return node.closest(INACTIVE_SELECTOR) !== null;
}

/**
 * Maps a key event to a page action, or null. Keys with Ctrl, Meta or Alt,
 * keys in a text entry, a Monaco editor, a dialog, a dock panel or an open
 * menu, keys from an inert or hidden pane, and keys while a modal dialog is
 * open give null. A modal dialog inside an inert or hidden pane is not open.
 */
export function workspaceKeyAction(
  event: Pick<
    KeyboardEvent,
    | 'key'
    | 'ctrlKey'
    | 'metaKey'
    | 'altKey'
    | 'shiftKey'
    | 'target'
    | 'defaultPrevented'
  >
): WorkspaceKeyAction | null {
  if (
    event.defaultPrevented ||
    event.ctrlKey ||
    event.metaKey ||
    event.altKey
  ) {
    return null;
  }
  const target = event.target;
  if (isTextEntryTarget(target)) return null;
  if (target instanceof Element) {
    if (inInactivePane(target) || target.closest(KEY_OWNER_SELECTOR)) {
      return null;
    }
  }
  const modals = document.querySelectorAll(OPEN_MODAL_SELECTOR);
  if (Array.from(modals).some((modal) => !inInactivePane(modal))) return null;
  switch (event.key) {
    case '[':
      return 'older';
    case ']':
      return 'newer';
    case 'L':
      return event.shiftKey ? 'latest' : null;
    case 'l':
      return event.shiftKey ? null : 'stepLog';
    case 'r':
      return event.shiftKey ? null : 'refresh';
    default:
      return null;
  }
}

/**
 * Binds the page keys: '[' and ']' select the older and newer run, Shift+L
 * follows the latest run, 'l' opens the dock on the log of the selected step
 * and 'r' refreshes the page. The history loads at the first '[' or ']'.
 * '[' and ']' go through the runs that the run timeline shows, the test runs
 * included when it shows them. The keys act the same in every mode. Needs WorkspaceDataProvider;
 * 'l' needs DockProvider.
 */
export function useWorkspaceKeys(): void {
  const run = useWorkspaceRun();
  const refresh = useWorkspaceRefresh();
  const dock = useDockCommands();
  const [url] = useWorkspaceUrl();
  const step = url.step;
  const [historyEnabled, setHistoryEnabled] = useState(false);
  const visible = useVisibleRuns({ enabled: historyEnabled });
  // A move asked before the history loaded; applied once it has.
  const [pending, setPending] = useState<'older' | 'newer' | null>(null);

  const move = (direction: 'older' | 'newer') => {
    const id = visible.adjacentId(direction);
    if (id) run.select(id);
  };

  const latest = useRef({
    run,
    refresh,
    visible,
    historyEnabled,
    move,
    dock,
    step,
  });
  useEffect(() => {
    latest.current = {
      run,
      refresh,
      visible,
      historyEnabled,
      move,
      dock,
      step,
    };
  });

  useEffect(() => {
    if (!pending || !historyEnabled || visible.isLoading) return;
    setPending(null);
    latest.current.move(pending);
  }, [pending, historyEnabled, visible.isLoading]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const action = workspaceKeyAction(event);
      if (!action) return;
      const current = latest.current;
      event.preventDefault();
      switch (action) {
        case 'refresh':
          void current.refresh().catch(() => undefined);
          return;
        case 'stepLog':
          if (current.step) current.dock.openStep(current.step);
          return;
        case 'latest':
          if (current.run.selection !== 'latest') current.run.select('latest');
          return;
        case 'older':
        case 'newer':
          if (!current.historyEnabled || current.visible.isLoading) {
            setHistoryEnabled(true);
            setPending(action);
            return;
          }
          current.move(action);
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, []);
}
