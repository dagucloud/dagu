// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement, ReactNode } from 'react';
import { AgentRunContext } from './agentRunContext';
import { ToolRunContext, type ToolRunInfo } from './toolRun';
import type { AgentRunInfo } from './types';

/** The facts of the shown run that the nodes of the canvas read. */
export function RunContexts({
  agent,
  tools,
  children,
}: {
  agent: AgentRunInfo | null;
  tools: ToolRunInfo;
  children: ReactNode;
}): ReactElement {
  return (
    <AgentRunContext.Provider value={agent}>
      <ToolRunContext.Provider value={tools}>
        {children}
      </ToolRunContext.Provider>
    </AgentRunContext.Provider>
  );
}
