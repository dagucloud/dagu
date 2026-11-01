// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo, type ComponentType } from 'react';
import type { DockTab } from '../../state/workspaceUrl';
import {
  ActionsTab,
  AgentTab,
  ArtifactsDockTab,
  ChatTab,
  ContinuousTab,
  LogTab,
  OutputsTab,
  TasksTab,
  TimelineTab,
} from './runTabBodies';
import type { DockTabBodyProps } from './types';

export type { DockTabBodyProps } from './types';

/**
 * The body of each dock tab. A tab without a body is not in the strip. Which
 * tabs a run has comes from dockTabsFor (runViewTabs), never from here. The
 * run tabs mount again when another run is selected. Each body is memoized:
 * the dock renders it again only when its props or its contexts change, not
 * when the panel opens or closes.
 */
export const dockTabBodies: Partial<
  Record<DockTab, ComponentType<DockTabBodyProps>>
> = {
  log: memo(LogTab),
  agent: memo(AgentTab),
  actions: memo(ActionsTab),
  timeline: memo(TimelineTab),
  outputs: memo(OutputsTab),
  artifacts: memo(ArtifactsDockTab),
  tasks: memo(TasksTab),
  chat: memo(ChatTab),
  continuous: memo(ContinuousTab),
};
