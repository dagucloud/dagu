// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext, useContext } from 'react';
import type { AgentRunInfo } from './types';

/**
 * The agent facts of the run that the canvas shows: null without a run, and
 * for a run of a DAG that is not an agent. The hub and the tool cards read it.
 */
export const AgentRunContext = createContext<AgentRunInfo | null>(null);

export function useAgentRun(): AgentRunInfo | null {
  return useContext(AgentRunContext);
}
