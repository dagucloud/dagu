import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { AppBarContext } from '@/contexts/AppBarContext';
import NotificationsPage from '..';

function renderPage() {
  const setTitle = vi.fn();

  render(
    <MemoryRouter>
      <AppBarContext.Provider value={{ setTitle } as never}>
        <NotificationsPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );

  return { setTitle };
}

describe('NotificationsPage', () => {
  it('renders notification links by section', () => {
    const { setTitle } = renderPage();

    expect(
      screen.getByRole('heading', { name: /^notifications$/i })
    ).toBeVisible();
    const rulesLink = screen.getByRole('link', { name: /^rules/i });
    const channelsLink = screen.getByRole('link', { name: /^channels/i });
    expect(rulesLink).toHaveAttribute('href', '/notification-rules');
    expect(channelsLink).toHaveAttribute('href', '/notification-channels');
    expect(
      rulesLink.compareDocumentPosition(channelsLink) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(
      screen.getByText('Set Global defaults and workspace overrides.')
    ).toBeVisible();
    expect(
      screen.getByText(
        'Manage Slack, email, webhook, and Telegram destinations.'
      )
    ).toBeVisible();
    expect(setTitle).toHaveBeenCalledWith('Notifications');
  });
});
