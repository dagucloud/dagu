// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useMemo } from 'react';
import { BucketKanbanList } from '@/features/cockpit/components/BucketKanbanList';
import type { KanbanFilters } from '@/features/cockpit/hooks/useDateKanbanData';
import type { View } from '@/hooks/useViews';

interface Props {
  view: View;
}

/**
 * Renders a saved view according to its render type. The switch on `view.type`
 * is the single extension point for future render types (timeline, table, ...).
 */
export function ViewPanel({ view }: Props): React.ReactElement {
  const filters = useMemo<KanbanFilters>(
    () => ({
      workspace: view.workspace ?? '',
      labels: view.labels ?? [],
      name: view.dagName ?? '',
    }),
    [view.workspace, view.labels, view.dagName]
  );

  switch (view.type) {
    case 'kanban':
    default:
      // Unknown/future types fall back to the Kanban renderer.
      return (
        <BucketKanbanList
          intervalDays={view.intervalDays}
          filters={filters}
          resetKey={view.id}
        />
      );
  }
}
