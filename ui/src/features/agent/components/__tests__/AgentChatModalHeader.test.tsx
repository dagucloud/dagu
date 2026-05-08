// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { AgentChatModalHeader } from '../AgentChatModalHeader';

function LocationDisplay(): React.ReactNode {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}</div>;
}

function renderHeader(onClose = vi.fn()) {
  return {
    onClose,
    ...render(
      <MemoryRouter initialEntries={['/dags']}>
        <UserPreferencesProvider>
          <AgentChatModalHeader
            sessionId={null}
            isSidebarOpen={false}
            onToggleSidebar={vi.fn()}
            onClearSession={vi.fn()}
            onClose={onClose}
          />
          <LocationDisplay />
        </UserPreferencesProvider>
      </MemoryRouter>
    ),
  };
}

describe('AgentChatModalHeader', () => {
  it('navigates to the agent page without closing the chat', () => {
    const { onClose } = renderHeader();

    fireEvent.click(
      screen.getByRole('button', { name: 'Open agent settings' })
    );

    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByTestId('location')).toHaveTextContent('/agent');
  });
});
