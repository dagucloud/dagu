// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Activity,
  Archive,
  Bot,
  ClipboardCheck,
  GanttChart,
  ListChecks,
  MessageSquare,
  Package,
  ScrollText,
  type LucideIcon,
} from 'lucide-react';
import { useMemo } from 'react';
import { TabStrip, type TabStripTab } from '@/components/ui/tab-strip';
import { useI18n } from '@/i18n/I18nProvider';
import type { DockTab } from '../state/workspaceUrl';

export const DOCK_TAB_LABELS: Record<DockTab, string> = {
  log: 'Log',
  agent: 'Agent',
  actions: 'Actions',
  timeline: 'Timeline',
  outputs: 'Outputs',
  artifacts: 'Artifacts',
  tasks: 'Tasks',
  chat: 'Chat',
  continuous: 'Continuous',
};

const DOCK_TAB_ICONS: Record<DockTab, LucideIcon> = {
  log: ScrollText,
  agent: Bot,
  actions: ClipboardCheck,
  timeline: GanttChart,
  outputs: Package,
  artifacts: Archive,
  tasks: ListChecks,
  chat: MessageSquare,
  continuous: Activity,
};

export const DOCK_ID_PREFIX = 'ww-dock';

/** The id of a tab button. Another strip on the page gives its own prefix. */
export function dockTabId(tab: DockTab, prefix = DOCK_ID_PREFIX): string {
  return `${prefix}-tab-${tab}`;
}

/** The id of the tab panel of a strip. */
export function dockPanelId(prefix = DOCK_ID_PREFIX): string {
  return `${prefix}-panel`;
}

export const DOCK_PANEL_ID = dockPanelId();

export interface DockTabStripProps {
  tabs: DockTab[];
  active: DockTab;
  badges: Partial<Record<DockTab, number>>;
  onSelect(tab: DockTab): void;
  /** Prefix of the ids, when a second strip is on the page. */
  idPrefix?: string;
  /** Accessible name of the tab list. Default: 'Logs'. */
  label?: string;
  /** Labels that replace the default ones, as a step view names its tabs. */
  labels?: Partial<Record<DockTab, string>>;
}

/**
 * Tab strip of the Run panel: the one TabStrip row. At a narrow width the
 * tabs show their icon only, with a tooltip, and the row scrolls if the icons
 * do not fit. There is no overflow menu and no select.
 */
export function DockTabStrip({
  tabs,
  active,
  badges,
  onSelect,
  idPrefix = DOCK_ID_PREFIX,
  label = 'Logs',
  labels,
}: DockTabStripProps) {
  const { ts } = useI18n();
  const panelId = dockPanelId(idPrefix);
  const stripTabs = useMemo<TabStripTab[]>(
    () =>
      tabs.map((tab) => ({
        id: tab,
        label: ts(labels?.[tab] ?? DOCK_TAB_LABELS[tab]),
        icon: DOCK_TAB_ICONS[tab],
        badge: badges[tab],
        badgeLabel: badges[tab]
          ? ts('{count} waiting', { count: badges[tab] })
          : undefined,
        controls: panelId,
      })),
    [tabs, badges, panelId, labels, ts]
  );
  return (
    <TabStrip
      label={ts(label)}
      tabs={stripTabs}
      value={active}
      onChange={(id) => onSelect(id as DockTab)}
      idPrefix={idPrefix}
      className="flex-1"
    />
  );
}
