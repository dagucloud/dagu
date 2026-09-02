// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useState, useCallback, useEffect, useId, useMemo } from 'react';
import { components, ViewColumn } from '@/api/v1/schema';
import { Tabs, Tab } from '@/components/ui/tabs';
import {
  normalizeViewColumns,
  VIEW_COLUMN_LABELS,
} from '@/features/views/viewColumns';
import { KanbanColumn } from './KanbanColumn';
import type { KanbanColumns } from '../hooks/useDateKanbanData';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

const STORAGE_KEY = 'dagu_cockpit_active_tab';

function getInitialTab(columns: readonly ViewColumn[]): ViewColumn {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored && columns.includes(stored as ViewColumn)) {
    return stored as ViewColumn;
  }
  return columns.includes(ViewColumn.running)
    ? ViewColumn.running
    : columns[0]!;
}

interface Props {
  columns: KanbanColumns;
  onCardClick: (run: DAGRunSummary) => void;
  onArtifactsClick: (run: DAGRunSummary) => void;
  visibleColumns?: readonly ViewColumn[];
}

export function MobileKanbanBoard({
  columns,
  onCardClick,
  onArtifactsClick,
  visibleColumns,
}: Props): React.ReactElement {
  const tabGroupId = useId();
  const columnOrder = useMemo(
    () => normalizeViewColumns(visibleColumns),
    [visibleColumns]
  );
  const [activeTab, setActiveTab] = useState<ViewColumn>(() =>
    getInitialTab(columnOrder)
  );

  useEffect(() => {
    if (!columnOrder.includes(activeTab)) {
      setActiveTab(getInitialTab(columnOrder));
    }
  }, [activeTab, columnOrder]);

  const handleTabChange = useCallback((key: ViewColumn) => {
    setActiveTab(key);
    localStorage.setItem(STORAGE_KEY, key);
  }, []);

  const tabId = (key: ViewColumn) => `${tabGroupId}-${key}-tab`;
  const tabPanelId = `${tabGroupId}-panel`;

  const handleTabKeyDown = (
    event: React.KeyboardEvent<HTMLElement>,
    key: ViewColumn
  ) => {
    const currentIndex = columnOrder.indexOf(key);
    let nextIndex: number | undefined;
    switch (event.key) {
      case 'ArrowLeft':
        nextIndex =
          (currentIndex - 1 + columnOrder.length) % columnOrder.length;
        break;
      case 'ArrowRight':
        nextIndex = (currentIndex + 1) % columnOrder.length;
        break;
      case 'Home':
        nextIndex = 0;
        break;
      case 'End':
        nextIndex = columnOrder.length - 1;
        break;
      default:
        return;
    }

    event.preventDefault();
    const nextColumn = columnOrder[nextIndex]!;
    handleTabChange(nextColumn);
    document.getElementById(tabId(nextColumn))?.focus();
  };

  return (
    <div className="flex min-h-0 flex-col">
      <I18nProps>
        <Tabs
          role="tablist"
          aria-label="Run status columns"
          className="mb-1 shrink-0 overflow-x-auto overflow-y-hidden border-b-0"
        >
          {columnOrder.map((key) => {
            const count = columns[key].runs.length;
            const countLabel = `${count}${columns[key].hasMore ? '+' : ''}`;
            return (
              <Tab
                key={key}
                id={tabId(key)}
                role="tab"
                isActive={activeTab === key}
                aria-selected={activeTab === key}
                aria-controls={tabPanelId}
                tabIndex={activeTab === key ? 0 : -1}
                onClick={() => handleTabChange(key)}
                onKeyDown={(event) => handleTabKeyDown(event, key)}
                className="h-8 px-2 text-xs"
              >
                <I18nText text={VIEW_COLUMN_LABELS[key]} />
                {(count > 0 || columns[key].hasMore) && (
                  <span className="ml-1 text-muted-foreground/60">
                    {countLabel}
                  </span>
                )}
              </Tab>
            );
          })}
        </Tabs>
      </I18nProps>
      <div
        id={tabPanelId}
        role="tabpanel"
        aria-labelledby={tabId(activeTab)}
        className="flex max-h-[70vh] min-h-0 flex-1 overflow-hidden"
      >
        <KanbanColumn
          title={VIEW_COLUMN_LABELS[activeTab]}
          column={columns[activeTab]}
          onCardClick={onCardClick}
          onArtifactsClick={onArtifactsClick}
          hideHeader
        />
      </div>
    </div>
  );
}
