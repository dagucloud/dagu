// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { createContext, useContext } from 'react';
import { AppBarContext } from './AppBarContext';

const RemoteNodeContext = createContext<string | undefined>(undefined);

function normalizeRemoteNode(value?: string): string | undefined {
  const normalized = value?.trim();
  return normalized ? normalized : undefined;
}

type RemoteNodeProviderProps = {
  remoteNode?: string;
  children: React.ReactNode;
};

export function RemoteNodeProvider({
  remoteNode,
  children,
}: RemoteNodeProviderProps) {
  const value = normalizeRemoteNode(remoteNode);
  return (
    <RemoteNodeContext.Provider value={value}>
      {children}
    </RemoteNodeContext.Provider>
  );
}

export function useRemoteNode(override?: string): string {
  const scopedRemoteNode = useContext(RemoteNodeContext);
  const appBarContext = useContext(AppBarContext);
  return (
    normalizeRemoteNode(override) ||
    normalizeRemoteNode(scopedRemoteNode) ||
    normalizeRemoteNode(appBarContext.selectedRemoteNode) ||
    'local'
  );
}
