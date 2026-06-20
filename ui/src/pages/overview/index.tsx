// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Tab, Tabs } from '@/components/ui/tabs';
import { useCanWrite } from '@/contexts/AuthContext';
import { ViewEditorDialog } from '@/features/views/ViewEditorDialog';
import { ViewPanel } from '@/features/views/ViewPanel';
import { View, useViews } from '@/hooks/useViews';
import { Clock3, Gauge, LayoutGrid, Pencil, Plus } from 'lucide-react';
import React from 'react';
import Dashboard from '..';
import CockpitPage from '../cockpit';

type BuiltinTab = 'timeline' | 'cockpit';
export type OverviewTab = BuiltinTab | `view:${string}`;

type OverviewPageProps = {
  initialTab?: BuiltinTab;
};

const OVERVIEW_ACTIVE_TAB_STORAGE_KEY = 'dagu_overview_active_tab';
const DEFAULT_OVERVIEW_TAB: OverviewTab = 'timeline';
const VIEW_TAB_PREFIX = 'view:';

function viewTabId(id: string): OverviewTab {
  return `${VIEW_TAB_PREFIX}${id}`;
}

function isBuiltinTab(value: string | null | undefined): value is BuiltinTab {
  return value === 'timeline' || value === 'cockpit';
}

function isViewTab(value: string | null | undefined): value is `view:${string}` {
  return typeof value === 'string' && value.startsWith(VIEW_TAB_PREFIX);
}

function viewIdFromTab(value: OverviewTab): string | null {
  return isViewTab(value) ? value.slice(VIEW_TAB_PREFIX.length) : null;
}

function tabElementId(tab: OverviewTab): string {
  return `overview-tab-${tab.replace(':', '-')}`;
}

function readStoredOverviewTab(): OverviewTab | null {
  try {
    const stored = localStorage.getItem(OVERVIEW_ACTIVE_TAB_STORAGE_KEY);
    if (isBuiltinTab(stored) || isViewTab(stored)) {
      return stored;
    }
    return null;
  } catch {
    return null;
  }
}

function getInitialTab(initialTab?: BuiltinTab): OverviewTab {
  return initialTab ?? readStoredOverviewTab() ?? DEFAULT_OVERVIEW_TAB;
}

export default function OverviewPage({
  initialTab,
}: OverviewPageProps): React.ReactElement {
  const { views, isLoading: viewsLoading } = useViews();
  const canWrite = useCanWrite();
  const [activeTab, setActiveTab] = React.useState<OverviewTab>(() =>
    getInitialTab(initialTab)
  );
  const [editorOpen, setEditorOpen] = React.useState(false);
  const [editingView, setEditingView] = React.useState<View | null>(null);

  React.useEffect(() => {
    if (initialTab) {
      setActiveTab(initialTab);
    }
  }, [initialTab]);

  React.useEffect(() => {
    try {
      localStorage.setItem(OVERVIEW_ACTIVE_TAB_STORAGE_KEY, activeTab);
    } catch {
      /* ignore */
    }
  }, [activeTab]);

  const activeViewId = viewIdFromTab(activeTab);
  const activeView = activeViewId
    ? views.find((v) => v.id === activeViewId)
    : undefined;

  // If the active view tab references a view that no longer exists once views
  // have loaded, fall back to the default tab instead of a blank panel.
  React.useEffect(() => {
    if (activeViewId && !viewsLoading && !activeView) {
      setActiveTab(DEFAULT_OVERVIEW_TAB);
    }
  }, [activeViewId, activeView, viewsLoading]);

  const openCreate = () => {
    setEditingView(null);
    setEditorOpen(true);
  };
  const openEdit = (view: View) => {
    setEditingView(view);
    setEditorOpen(true);
  };

  let panel: React.ReactNode;
  if (activeView) {
    panel = (
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex items-center justify-between px-1 pb-2">
          <h2 className="truncate text-sm font-semibold text-foreground">
            {activeView.name}
          </h2>
          {canWrite && (
            <button
              type="button"
              onClick={() => openEdit(activeView)}
              className="inline-flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-muted-foreground hover:text-foreground"
            >
              <Pencil className="h-3 w-3" /> Edit
            </button>
          )}
        </div>
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
          <ViewPanel view={activeView} />
        </div>
      </div>
    );
  } else if (activeTab === 'cockpit') {
    panel = <CockpitPage />;
  } else {
    panel = <Dashboard />;
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Tabs
        role="tablist"
        aria-label="Overview views"
        className="mb-3 shrink-0 overflow-x-auto"
      >
        <Tab
          id={tabElementId('timeline')}
          role="tab"
          aria-selected={activeTab === 'timeline'}
          aria-controls="overview-panel"
          isActive={activeTab === 'timeline'}
          onClick={() => setActiveTab('timeline')}
          className="cursor-pointer gap-2"
        >
          <Clock3 className="h-4 w-4" />
          Timeline
        </Tab>
        <Tab
          id={tabElementId('cockpit')}
          role="tab"
          aria-selected={activeTab === 'cockpit'}
          aria-controls="overview-panel"
          isActive={activeTab === 'cockpit'}
          onClick={() => setActiveTab('cockpit')}
          className="cursor-pointer gap-2"
        >
          <Gauge className="h-4 w-4" />
          Cockpit
        </Tab>
        {views.map((view) => {
          const tab = viewTabId(view.id);
          return (
            <Tab
              key={view.id}
              id={tabElementId(tab)}
              role="tab"
              aria-selected={activeTab === tab}
              aria-controls="overview-panel"
              isActive={activeTab === tab}
              onClick={() => setActiveTab(tab)}
              className="cursor-pointer gap-2"
            >
              <LayoutGrid className="h-4 w-4" />
              {view.name}
            </Tab>
          );
        })}
        {canWrite && (
          <button
            type="button"
            onClick={openCreate}
            aria-label="Create view"
            className="inline-flex h-12 cursor-pointer items-center gap-1 px-3 text-sm font-medium text-text-secondary hover:text-foreground"
          >
            <Plus className="h-4 w-4" />
            New
          </button>
        )}
      </Tabs>

      <div
        id="overview-panel"
        role="tabpanel"
        aria-labelledby={tabElementId(activeTab)}
        className="min-h-0 flex-1 overflow-hidden"
      >
        {panel}
      </div>

      <ViewEditorDialog
        open={editorOpen}
        onOpenChange={setEditorOpen}
        view={editingView}
        onSaved={(saved) => setActiveTab(viewTabId(saved.id))}
        onDeleted={() => setActiveTab(DEFAULT_OVERVIEW_TAB)}
      />
    </div>
  );
}
