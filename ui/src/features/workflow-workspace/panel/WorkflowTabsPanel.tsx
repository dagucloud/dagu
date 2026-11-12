// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertTriangle,
  Bell,
  BookOpen,
  LayoutList,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useMemo, type ReactNode } from 'react';
import DAGWikiTab from '@/features/dags/components/dag-details/DAGWikiTab';
import IncidentsTab from '@/features/dags/components/dag-details/IncidentsTab';
import NotificationsTab from '@/features/dags/components/dag-details/NotificationsTab';
import { TabStrip, tabStripTabId } from '@/components/ui/tab-strip';
import { PANEL_BODY_CLASS } from '@/features/dags/components/step-details/PanelShell';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ApiDAGDetails } from '../contracts';
import { RuntimeProfileSection } from '../profile/RuntimeProfileSection';
import { useSettingsLeaveGuard } from '../settings/useSettingsLeaveGuard';
import { useWorkspaceUrl, type WorkflowPanelTab } from '../state/workspaceUrl';

export interface WorkflowTabInfo {
  id: WorkflowPanelTab;
  /** Static message key. */
  label: string;
  icon: LucideIcon;
}

/** The tabs of the Workflow panel, in order. */
export const WORKFLOW_TABS: readonly WorkflowTabInfo[] = [
  { id: 'overview', label: 'Overview', icon: LayoutList },
  { id: 'wiki', label: 'Wiki', icon: BookOpen },
  { id: 'incidents', label: 'Incidents', icon: AlertTriangle },
  { id: 'notifications', label: 'Notifications', icon: Bell },
];

const TAB_ID_PREFIX = 'ww-wtab';
const tabId = (t: WorkflowPanelTab) => tabStripTabId(TAB_ID_PREFIX, t);
const panelId = (t: WorkflowPanelTab) => `ww-wtab-panel-${t}`;

export interface WorkflowTabsPanelProps {
  /** The overview of the workflow, without its schedule. The runtime profile follows it. */
  overview: ReactNode;
  dag: ApiDAGDetails;
  fileName: string;
  workspaceName: string | null;
}

/**
 * The content of the Workflow panel: a tab strip under the title bar, then
 * the body of the active tab. The active tab is the `wtab` of the URL, and a
 * change replaces the URL. The tabs are the one TabStrip row: the arrow keys,
 * Home and End open a tab. A change goes through the leave guard, as the
 * notification, incident and runtime profile drafts are at risk. The body is
 * a query container: the layout of a tab follows the width of the panel and
 * not the width of the window.
 */
export function WorkflowTabsPanel({
  overview,
  dag,
  fileName,
  workspaceName,
}: WorkflowTabsPanelProps) {
  const { ts } = useI18n();
  const [url, setUrl] = useWorkspaceUrl();
  const { guard, setDirty } = useSettingsLeaveGuard();
  const tab: WorkflowPanelTab = url.wtab ?? 'overview';
  const stripTabs = useMemo(
    () =>
      WORKFLOW_TABS.map(({ id, label, icon }) => ({
        id,
        label: ts(label),
        icon,
        controls: panelId(id),
      })),
    [ts]
  );

  // A tab that goes away takes its draft with it.
  useEffect(
    () => () => {
      setDirty('notifications', false);
      setDirty('incidents', false);
    },
    [tab, setDirty]
  );

  const requestTab = (next: WorkflowPanelTab) => {
    if (next === tab) return;
    guard(() =>
      setUrl({ wtab: next === 'overview' ? null : next }, { replace: true })
    );
  };

  const workspace = workspaceName ?? undefined;

  return (
    <div
      data-testid="ww-workflow-tabs"
      className="flex min-h-0 flex-1 flex-col"
    >
      <div className="flex min-w-0 shrink-0 border-b border-border px-2">
        <TabStrip
          label={ts('Workflow sections')}
          tabs={stripTabs}
          value={tab}
          onChange={(id) => requestTab(id as WorkflowPanelTab)}
          idPrefix={TAB_ID_PREFIX}
          className="flex-1"
        />
      </div>
      <div
        role="tabpanel"
        id={panelId(tab)}
        aria-labelledby={tabId(tab)}
        data-testid={`ww-wtab-body-${tab}`}
        className={cn(
          PANEL_BODY_CLASS,
          '@container',
          tab === 'overview' && 'space-y-6'
        )}
      >
        {tab === 'overview' && (
          <>
            {overview}
            <RuntimeProfileSection
              fileName={fileName}
              workspaceName={workspaceName}
            />
          </>
        )}
        {tab === 'wiki' && (
          <DAGWikiTab dagName={dag.name} workspaceName={workspace} />
        )}
        {tab === 'incidents' && (
          <IncidentsTab
            fileName={fileName}
            workspaceName={workspace}
            onDirtyChange={(dirty) => setDirty('incidents', dirty)}
          />
        )}
        {tab === 'notifications' && (
          <NotificationsTab
            fileName={fileName}
            workspaceName={workspace}
            onDirtyChange={(dirty) => setDirty('notifications', dirty)}
          />
        )}
      </div>
    </div>
  );
}
