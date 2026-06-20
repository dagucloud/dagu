// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import OverviewPage from '..';
import { useViews } from '@/hooks/useViews';

vi.mock('../..', () => ({
  default: () => <div>Timeline panel</div>,
}));

vi.mock('../../cockpit', () => ({
  default: () => <div>Cockpit panel</div>,
}));

// Stub the views feature modules so importing the page does not pull in the
// real API client (which expects a build-time getConfig global).
vi.mock('@/hooks/useViews', () => ({
  useViews: vi.fn(),
}));

vi.mock('@/features/views/ViewEditorDialog', () => ({
  ViewEditorDialog: () => null,
}));

vi.mock('@/features/views/ViewPanel', () => ({
  ViewPanel: ({ view }: { view: { name: string } }) => (
    <div>View panel: {view.name}</div>
  ),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWrite: () => true,
  useCanWriteForWorkspace: () => true,
}));

const useViewsMock = vi.mocked(useViews);

function mockViews(views: Array<{ id: string; name: string }>) {
  useViewsMock.mockReturnValue({
    views,
    isLoading: false,
    error: null,
    createView: vi.fn(),
    updateView: vi.fn(),
    deleteView: vi.fn(),
    refresh: vi.fn(),
  } as unknown as ReturnType<typeof useViews>);
}

describe('OverviewPage', () => {
  beforeEach(() => {
    localStorage.clear();
    mockViews([]);
  });

  it('defaults to the Timeline tab', () => {
    render(<OverviewPage />);

    expect(screen.getByRole('tab', { name: /timeline/i })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByText('Timeline panel')).toBeInTheDocument();
    expect(screen.queryByText('Cockpit panel')).not.toBeInTheDocument();
  });

  it('persists the last selected tab', async () => {
    const user = userEvent.setup();
    const { unmount } = render(<OverviewPage />);

    await user.click(screen.getByRole('tab', { name: /cockpit/i }));

    expect(screen.getByRole('tab', { name: /cockpit/i })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(localStorage.getItem('dagu_overview_active_tab')).toBe('cockpit');

    unmount();
    render(<OverviewPage />);

    expect(screen.getByRole('tab', { name: /cockpit/i })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByText('Cockpit panel')).toBeInTheDocument();
  });

  it('honors an explicit route tab over stored state', () => {
    localStorage.setItem('dagu_overview_active_tab', 'cockpit');

    render(<OverviewPage initialTab="timeline" />);

    expect(screen.getByRole('tab', { name: /timeline/i })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByText('Timeline panel')).toBeInTheDocument();
  });

  it('renders a tab per saved view and shows its panel when selected', async () => {
    const user = userEvent.setup();
    mockViews([{ id: 'v1', name: 'Prod board' }]);

    render(<OverviewPage />);

    const viewTab = screen.getByRole('tab', { name: /prod board/i });
    expect(viewTab).toBeInTheDocument();

    await user.click(viewTab);

    expect(viewTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('View panel: Prod board')).toBeInTheDocument();
    expect(localStorage.getItem('dagu_overview_active_tab')).toBe('view:v1');
  });
});
