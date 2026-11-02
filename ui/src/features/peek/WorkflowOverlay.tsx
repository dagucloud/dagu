// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { useLocation, useNavigate, type Location } from 'react-router-dom';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { peekPageURL, samePeekTarget, type PeekTarget } from './peekParams';

/** A workflow page, at /dags/:fileName with any tab. The list is /dags/. */
const WORKFLOW_PATH = /^\/dags\/[^/]+/;

export interface WorkflowOverlay {
  /**
   * The location of the page below the workflow page while the overlay is
   * open; the routes render it in place of the URL. Null otherwise.
   */
  background: Location | null;
  isOpen: boolean;
  /** Opens the workflow page of the target over the current page. */
  open(target: PeekTarget): void;
  /** Goes back to the page below. */
  close(): void;
  /** Keeps the workflow page as the page itself. */
  expand(): void;
  /**
   * Sets the targets of the rows of the page below, in their order: the
   * overlay steps through them.
   */
  setSequence(targets: readonly PeekTarget[]): void;
  /** True when a row comes before or after the shown one: -1 and 1. */
  canStep(delta: -1 | 1): boolean;
  /** Shows the row before or after the shown one in place of it. */
  step(delta: -1 | 1): void;
  /** The row that the overlay shows; null while it is closed. */
  target: PeekTarget | null;
  /**
   * The row that the overlay shows, or showed last before it closed: the
   * list marks it. Null before the overlay first opens.
   */
  shown: PeekTarget | null;
}

const WorkflowOverlayContext = createContext<WorkflowOverlay | null>(null);

/** Null without a WorkflowOverlayProvider above. */
export function useWorkflowOverlay(): WorkflowOverlay | null {
  return useContext(WorkflowOverlayContext);
}

interface Opened {
  background: Location;
  /** The history index of the page below, which close goes back to. */
  index: number | null;
  /** The row that the overlay shows. */
  target: PeekTarget;
  /**
   * True from the click until the URL names the workflow page: the overlay
   * shows at once, and the page fills it when it is ready.
   */
  pending: boolean;
}

/** The index that the router keeps in the history state of each entry. */
function historyIndex(): number | null {
  const index = (window.history.state as { idx?: unknown } | null)?.idx;
  return typeof index === 'number' ? index : null;
}

/**
 * Shows the workflow page over the page that opened it. The workflow page
 * runs at its own URL, with all its links and keys; the page below keeps
 * the location it had. The overlay ends when the URL leaves the workflow
 * pages: Back to the page below, a link to another page, or close. A reload
 * shows the workflow page alone.
 */
export function WorkflowOverlayProvider({
  children,
}: {
  children: ReactNode;
}): ReactElement {
  const location = useLocation();
  const navigate = useNavigate();
  const remoteNode = useRemoteNode();
  const [opened, setOpened] = useState<Opened | null>(null);
  const [lastShown, setLastShown] = useState<PeekTarget | null>(null);
  const onWorkflow = WORKFLOW_PATH.test(location.pathname);
  const openedTarget = opened?.target ?? null;
  if (
    openedTarget &&
    (!lastShown || !samePeekTarget(lastShown, openedTarget))
  ) {
    setLastShown(openedTarget);
  }
  // Another page than the one below ends the overlay. The page below itself
  // keeps it, so that Forward shows the workflow over it again.
  if (opened && location.key !== opened.background.key) {
    if (!onWorkflow) setOpened(null);
    else if (opened.pending) setOpened({ ...opened, pending: false });
  }
  const locationRef = useRef(location);
  locationRef.current = location;
  const openedRef = useRef(opened);
  openedRef.current = opened;

  const [sequence, setSequenceState] = useState<readonly PeekTarget[]>([]);
  const sequenceRef = useRef(sequence);
  sequenceRef.current = sequence;
  const setSequence = useCallback(
    (targets: readonly PeekTarget[]) => setSequenceState(targets),
    []
  );

  const open = useCallback(
    (target: PeekTarget) => {
      setOpened({
        background: locationRef.current,
        index: historyIndex(),
        target,
        pending: true,
      });
      navigate(peekPageURL(target, remoteNode));
    },
    [navigate, remoteNode]
  );

  // A step replaces the entry of the shown row, so that close and Back
  // still go to the page below.
  const step = useCallback(
    (delta: -1 | 1) => {
      const current = openedRef.current;
      if (!current) return;
      const rows = sequenceRef.current;
      const at = rows.findIndex((row) => samePeekTarget(row, current.target));
      const next = at === -1 ? undefined : rows[at + delta];
      if (!next) return;
      setOpened({ ...current, target: next });
      navigate(peekPageURL(next, remoteNode), { replace: true });
    },
    [navigate, remoteNode]
  );

  const close = useCallback(() => {
    const current = openedRef.current;
    if (!current) return;
    const now = historyIndex();
    if (current.index !== null && now !== null && now > current.index) {
      navigate(current.index - now);
      return;
    }
    navigate(current.background);
  }, [navigate]);

  const expand = useCallback(() => setOpened(null), []);

  const isOpen = opened !== null && (onWorkflow || opened.pending);
  const at =
    isOpen && opened
      ? sequence.findIndex((row) => samePeekTarget(row, opened.target))
      : -1;
  const value = useMemo<WorkflowOverlay>(
    () => ({
      background: isOpen && opened ? opened.background : null,
      isOpen,
      open,
      close,
      expand,
      setSequence,
      canStep: (delta) => at !== -1 && sequence[at + delta] !== undefined,
      step,
      target: isOpen && opened ? opened.target : null,
      shown: isOpen && opened ? opened.target : lastShown,
    }),
    [
      isOpen,
      opened,
      open,
      close,
      expand,
      setSequence,
      at,
      sequence,
      step,
      lastShown,
    ]
  );
  return (
    <WorkflowOverlayContext.Provider value={value}>
      {children}
    </WorkflowOverlayContext.Provider>
  );
}
