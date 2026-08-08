// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { createContext, useContext } from 'react';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

export type DagLookup =
  | { state: 'loading' }
  | { state: 'not-found' }
  | {
      state: 'found';
      fileName: string;
      dagName: string;
      latestDAGRun: DAGRunSummary;
      suspended: boolean;
      nextRun?: string;
    };

export type DocLiveContextValue = {
  /** Workspace the surrounding document belongs to. */
  workspace: string | null;
  remoteNode: string;
  /** Register interest in a DAG reference; returns an unregister callback. */
  registerRef: (ref: string) => () => void;
  /** Resolve a DAG reference (name first, then file name) to live data. */
  lookup: (ref: string) => DagLookup;
};

export const DocLiveContext = createContext<DocLiveContextValue | null>(null);

/** Live DAG data for doc-embedded UI; null outside a DocLiveProvider. */
export function useDocLive(): DocLiveContextValue | null {
  return useContext(DocLiveContext);
}
