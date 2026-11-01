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
import { peekPageURL, type PeekTarget } from './peekParams';

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
  const onWorkflow = WORKFLOW_PATH.test(location.pathname);
  // Another page than the one below ends the overlay. The page below itself
  // keeps it, so that Forward shows the workflow over it again.
  if (opened && !onWorkflow && location.key !== opened.background.key) {
    setOpened(null);
  }
  const locationRef = useRef(location);
  locationRef.current = location;
  const openedRef = useRef(opened);
  openedRef.current = opened;

  const open = useCallback(
    (target: PeekTarget) => {
      setOpened({ background: locationRef.current, index: historyIndex() });
      navigate(peekPageURL(target, remoteNode));
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

  const isOpen = opened !== null && onWorkflow;
  const value = useMemo<WorkflowOverlay>(
    () => ({
      background: isOpen && opened ? opened.background : null,
      isOpen,
      open,
      close,
      expand,
    }),
    [isOpen, opened, open, close, expand]
  );
  return (
    <WorkflowOverlayContext.Provider value={value}>
      {children}
    </WorkflowOverlayContext.Provider>
  );
}
