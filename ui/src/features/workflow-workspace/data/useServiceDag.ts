// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext } from 'react';
import { isServiceDAG } from '@/lib/dagSchedule';
import { WorkspaceDataContext } from './context';

/**
 * True when the workflow of the page is a service DAG (restart_policy).
 * False outside a workspace page.
 */
export function useIsServiceDag(): boolean {
  const dag = useContext(WorkspaceDataContext)?.dagData.dag;
  return !!dag && isServiceDAG(dag);
}
