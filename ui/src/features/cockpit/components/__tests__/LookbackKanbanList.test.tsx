// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { KanbanFilters } from '../../hooks/useDateKanbanData';
import { LookbackKanbanList } from '../LookbackKanbanList';

type CapturedProps = { date: string; todayStr: string; filters?: KanbanFilters };
const sectionProps: CapturedProps[] = [];

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tzOffsetInSec: 0 }),
}));

vi.mock(
  '@/features/dag-runs/components/dag-run-details/DAGRunDetailsModal',
  () => ({ default: () => null })
);

vi.mock('../ArtifactListModal', () => ({
  ArtifactListModal: () => null,
}));

vi.mock('../DateKanbanSection', () => ({
  DateKanbanSection: (props: CapturedProps) => {
    sectionProps.push(props);
    return <div data-testid="section">{props.date}</div>;
  },
}));

beforeEach(() => {
  sectionProps.length = 0;
});

afterEach(() => {
  cleanup();
});

describe('LookbackKanbanList', () => {
  it('renders one section per lookback day and forwards filters', () => {
    const filters: KanbanFilters = {
      workspace: 'prod',
      labels: ['team=a'],
      name: 'etl',
    };
    render(<LookbackKanbanList lookbackDays={3} filters={filters} />);

    expect(screen.getAllByTestId('section')).toHaveLength(3);
    expect(sectionProps).toHaveLength(3);
    for (const props of sectionProps) {
      expect(props.filters).toEqual(filters);
    }
    // Sections run from newest (today) to oldest, strictly descending.
    const dates = sectionProps.map((p) => p.date);
    expect([...dates].sort((a, b) => b.localeCompare(a))).toEqual(dates);
  });

  it('clamps the lookback window to 30 days', () => {
    render(<LookbackKanbanList lookbackDays={99} filters={{}} />);
    expect(screen.getAllByTestId('section')).toHaveLength(30);
  });

  it('always renders at least one day', () => {
    render(<LookbackKanbanList lookbackDays={0} filters={{}} />);
    expect(screen.getAllByTestId('section')).toHaveLength(1);
  });
});
