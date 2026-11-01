// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo, type ReactElement, type ReactNode } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { PeekContext, type PeekController } from './PeekContext';
import { peekPageURL, readPeek } from './peekParams';
import { useWorkflowOverlay } from './WorkflowOverlay';

/**
 * Lets the lists below it open a workflow or a run over the list: the
 * workflow page in the overlay of the app. Without an overlay above, the
 * rows open the full page. A link with ?peek, from a version that had a
 * preview in its place, opens the workflow page.
 */
export function PeekHost({ children }: { children: ReactNode }): ReactElement {
  const overlay = useWorkflowOverlay();
  const location = useLocation();
  const navigate = useNavigate();
  const remoteNode = useRemoteNode();

  const linked = useMemo(
    () => readPeek(new URLSearchParams(location.search)),
    [location.search]
  );
  useEffect(() => {
    if (linked) navigate(peekPageURL(linked, remoteNode), { replace: true });
  }, [linked, navigate, remoteNode]);

  const open = overlay?.open;
  const isOpen = overlay?.isOpen ?? false;
  const setSequence = overlay?.setSequence;
  const controller = useMemo<PeekController | null>(
    () => (open ? { open, isOpen, setSequence } : null),
    [open, isOpen, setSequence]
  );
  return (
    <PeekContext.Provider value={controller}>{children}</PeekContext.Provider>
  );
}
