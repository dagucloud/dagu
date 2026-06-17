// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { createContext, useContext } from 'react';
import { AppBarContext } from './AppBarContext';

const RemoteNodeContext = createContext<string | undefined>(undefined);

type RemoteNodeProviderProps = {
  remoteNode?: string;
  children: React.ReactNode;
};

export function RemoteNodeProvider({
  remoteNode,
  children,
}: RemoteNodeProviderProps) {
  const value = remoteNode && remoteNode.trim() ? remoteNode : undefined;
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
    override || scopedRemoteNode || appBarContext.selectedRemoteNode || 'local'
  );
}
