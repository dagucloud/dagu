// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkspaceDeprecationItemKind } from '@/api/v1/schema';
import { useIsAdmin } from '@/contexts/AuthContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { WorkspaceDeprecationNotice } from '../WorkspaceDeprecationNotice';

// The generated query hook's type is too deep to mock through vi.mocked.
const { useQueryMock } = vi.hoisted(() => ({ useQueryMock: vi.fn() }));
vi.mock('@/hooks/api', () => ({
  useQuery: useQueryMock,
}));

vi.mock('@/contexts/AuthContext', () => ({
  useIsAdmin: vi.fn(),
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const useIsAdminMock = vi.mocked(useIsAdmin);

const iconName = 'Workspaces become projects in v3.0';
const billing = {
  kind: WorkspaceDeprecationItemKind.baseConfig,
  name: 'billing',
};
const payments = {
  kind: WorkspaceDeprecationItemKind.baseConfig,
  name: 'payments',
};
const alice = { kind: WorkspaceDeprecationItemKind.user, name: 'alice' };

function respondWith(
  items: { kind: WorkspaceDeprecationItemKind; name: string }[]
) {
  useQueryMock.mockReturnValue({ data: { items } });
}

function renderNotice() {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <WorkspaceDeprecationNotice />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('WorkspaceDeprecationNotice', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useIsAdminMock.mockReturnValue(true);
  });

  it('lists the settings by kind once an admin opens it', async () => {
    respondWith([billing, payments, alice]);

    renderNotice();

    expect(screen.queryByText('alice')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: iconName }));

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Base configbilling, payments');
    expect(dialog).toHaveTextContent('Useralice');
    expect(
      screen.getByRole('link', {
        name: 'Have feedback? Comment on the GitHub issue.',
      })
    ).toHaveAttribute('href', 'https://github.com/dagucloud/dagu/issues/3288');
  });

  it('stays hidden after a dismissal until another setting appears', async () => {
    respondWith([billing]);
    const first = renderNotice();

    await userEvent.click(screen.getByRole('button', { name: iconName }));
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }));

    expect(
      screen.queryByRole('button', { name: iconName })
    ).not.toBeInTheDocument();

    // A later visit reads the dismissal back from the browser.
    first.unmount();
    const second = renderNotice();
    expect(
      screen.queryByRole('button', { name: iconName })
    ).not.toBeInTheDocument();

    second.unmount();
    respondWith([billing, payments]);
    renderNotice();
    expect(screen.getByRole('button', { name: iconName })).toBeInTheDocument();
  });

  it('renders nothing when no workspace setting is in use', () => {
    respondWith([]);

    const { container } = renderNotice();

    expect(container).toBeEmptyDOMElement();
  });

  it('does not ask for the list for a non-admin', () => {
    useIsAdminMock.mockReturnValue(false);
    respondWith([]);

    const { container } = renderNotice();

    expect(container).toBeEmptyDOMElement();
    expect(useQueryMock).toHaveBeenCalledWith('/workspaces/deprecation', null);
  });
});
