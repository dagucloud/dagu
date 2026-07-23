// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';

import LoadingIndicator from '@/components/ui/loading-indicator';
import { AppBarContext } from '@/contexts/AppBarContext';
import { WorkspaceKind } from '@/lib/workspace';

export function LocalControllerScope({
  children,
}: {
  children: React.ReactElement;
}) {
  const appBar = React.useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode.trim() || 'local';
  const hasRenderedLocally = React.useRef(remoteNode === 'local');

  React.useEffect(() => {
    if (remoteNode === 'local') {
      hasRenderedLocally.current = true;
      return;
    }
    appBar.selectWorkspace?.({ kind: WorkspaceKind.all });
    appBar.selectRemoteNode('local');
  }, [appBar, remoteNode]);

  return remoteNode === 'local' || hasRenderedLocally.current ? (
    children
  ) : (
    <LoadingIndicator />
  );
}
