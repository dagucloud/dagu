// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AppBarContext } from '@/contexts/AppBarContext';
import { NotificationEventType } from '@/api/v1/schema';

vi.hoisted(() => {
  vi.stubGlobal('getConfig', () => ({
    apiURL: '/api/v1',
    authMode: 'builtin',
  }));
});

const mocks = vi.hoisted(() => ({
  useQuery: vi.fn(),
  client: {
    PUT: vi.fn(),
    POST: vi.fn(),
    DELETE: vi.fn(),
  },
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: mocks.useQuery,
}));

import NotificationsPage, {
  NotificationChannelsPage,
  NotificationRulesPage,
} from '..';

Object.defineProperty(HTMLElement.prototype, 'hasPointerCapture', {
  configurable: true,
  value: () => false,
});

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

function renderChannelsPage(settings: object, channels: object[] = []) {
  const settingsQuery = {
    data: settings,
    error: undefined,
    isLoading: false,
    mutate: vi.fn(),
  };
  const channelsQuery = {
    data: { channels },
    error: undefined,
    isLoading: false,
    mutate: vi.fn(),
  };
  mocks.useQuery.mockImplementation((path: string) =>
    path === '/notification-settings' ? settingsQuery : channelsQuery
  );

  render(
    <MemoryRouter>
      <AppBarContext.Provider
        value={
          {
            setTitle: vi.fn(),
            selectedRemoteNode: 'local',
          } as never
        }
      >
        <NotificationChannelsPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

function renderRulesPage(
  extraChannels: object[] = [],
  events = [
    NotificationEventType.dag_run_aborted,
    NotificationEventType.dag_run_rejected,
  ],
  workspaceName?: string
) {
  const queries: Record<string, object> = {
    '/notification-channels': {
      channels: [
        { id: 'slack', name: 'slack-test', type: 'slack', enabled: true },
        ...extraChannels,
      ],
    },
    '/notification-routes/workspaces/{workspaceName}': {
      enabled: true,
      inheritGlobal: true,
      routes: [],
    },
    '/notification-routes/global': {
      enabled: true,
      inheritGlobal: true,
      routes: [
        {
          id: 'route',
          channelId: 'slack',
          enabled: true,
          events,
        },
      ],
    },
  };
  const savedQueries = new Map<string, object>();
  mocks.useQuery.mockImplementation((path: string, init: object) => {
    const key = JSON.stringify([path, init]);
    return {
      data: savedQueries.get(key) ?? queries[path],
      isLoading: false,
      mutate: vi.fn((data: object) => savedQueries.set(key, data)),
    };
  });

  const page = (workspaceName?: string, remoteNode = 'local') => (
    <MemoryRouter>
      <AppBarContext.Provider
        value={
          {
            setTitle: vi.fn(),
            selectedRemoteNode: remoteNode,
            workspaceSelection: workspaceName
              ? { kind: 'workspace', workspace: workspaceName }
              : { kind: 'all' },
          } as never
        }
      >
        <NotificationRulesPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  const view = render(page(workspaceName));
  return {
    changeScope: (workspaceName?: string, remoteNode = 'local') =>
      view.rerender(page(workspaceName, remoteNode)),
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.client.PUT.mockImplementation(
    async (_path: string, { body }: { body: object }) => ({ data: body })
  );
});

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

describe('NotificationChannelsPage', () => {
  const slackChannel = {
    id: 'slack',
    name: 'slack-test',
    type: 'slack',
    enabled: true,
    slack: {
      webhookUrlConfigured: true,
      webhookUrlPreview: 'https://hooks.slack.com/services/***',
    },
  };

  it('keeps a pending channel save on its original remote node', async () => {
    const user = userEvent.setup();
    let completeSave!: (response: object) => void;
    mocks.client.POST.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          completeSave = resolve;
        })
    );
    const settingsQuery = { data: {}, isLoading: false, mutate: vi.fn() };
    const channelsQuery = {
      data: { channels: [] },
      isLoading: false,
      mutate: vi.fn(),
    };
    mocks.useQuery.mockImplementation((path: string) =>
      path === '/notification-settings' ? settingsQuery : channelsQuery
    );
    const page = (remoteNode: string) => (
      <MemoryRouter>
        <AppBarContext.Provider
          value={{ setTitle: vi.fn(), selectedRemoteNode: remoteNode } as never}
        >
          <NotificationChannelsPage />
        </AppBarContext.Provider>
      </MemoryRouter>
    );
    const view = render(page('local'));
    await user.click(screen.getByRole('button', { name: 'Add channel' }));
    const editor = screen.getByRole('dialog');
    await user.type(
      within(editor).getByLabelText('Channel name'),
      'Local alerts'
    );
    await user.type(
      within(editor).getByLabelText('Slack webhook URL'),
      'https://hooks.slack.com/services/test'
    );
    await user.click(
      within(editor).getByRole('button', { name: 'Create channel' })
    );
    expect(mocks.client.POST).toHaveBeenCalledWith(
      '/notification-channels',
      expect.objectContaining({
        params: { query: { remoteNode: 'local' } },
      })
    );

    view.rerender(page('remote'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await act(async () => {
      completeSave({ data: { ...slackChannel, name: 'Local alerts' } });
    });
    expect(
      screen.queryByRole('listitem', { name: 'Local alerts' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add channel' })).toBeEnabled();
  });

  it('searches saved channels by name and provider', async () => {
    const user = userEvent.setup();
    renderChannelsPage({}, [
      slackChannel,
      {
        id: 'email',
        name: 'Ops email',
        type: 'email',
        enabled: false,
        email: { to: ['ops@example.com'] },
      },
    ]);
    const search = screen.getByRole('searchbox', { name: 'Search channels' });
    await user.type(search, 'SLACK');
    expect(screen.getByRole('listitem', { name: 'slack-test' })).toBeVisible();
    expect(
      screen.queryByRole('listitem', { name: 'Ops email' })
    ).not.toBeInTheDocument();
    await user.clear(search);
    await user.type(search, 'unknown');
    expect(screen.getByText('No matching channels')).toBeVisible();
    await user.clear(search);
    expect(screen.getByRole('listitem', { name: 'Ops email' })).toBeVisible();
  });

  it('edits one channel and preserves its saved secret', async () => {
    const user = userEvent.setup();
    mocks.client.PUT.mockResolvedValue({
      data: { ...slackChannel, name: 'Production alerts' },
    });
    renderChannelsPage({}, [slackChannel]);
    await user.click(screen.getByRole('button', { name: 'Edit' }));
    const dialog = within(screen.getByRole('dialog'));
    await user.clear(dialog.getByLabelText('Channel name'));
    await user.type(dialog.getByLabelText('Channel name'), 'Production alerts');
    await user.click(dialog.getByRole('button', { name: 'Save changes' }));
    expect(mocks.client.PUT).toHaveBeenCalledWith(
      '/notification-channels/{channelId}',
      expect.objectContaining({
        params: {
          path: { channelId: 'slack' },
          query: { remoteNode: 'local' },
        },
        body: expect.objectContaining({
          name: 'Production alerts',
          slack: expect.objectContaining({ webhookUrl: undefined }),
        }),
      })
    );
    expect(
      screen.getByRole('listitem', { name: 'Production alerts' })
    ).toBeVisible();
  });

  it('cancels channel edits and creation without saving', async () => {
    const user = userEvent.setup();
    renderChannelsPage({}, [slackChannel]);
    await user.click(screen.getByRole('button', { name: 'Edit' }));
    await user.clear(screen.getByLabelText('Channel name'));
    await user.type(screen.getByLabelText('Channel name'), 'Unsaved name');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await user.click(screen.getByRole('button', { name: 'Edit' }));
    expect(screen.getByLabelText('Channel name')).toHaveValue('slack-test');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await user.click(screen.getByRole('button', { name: 'Add channel' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    expect(mocks.client.PUT).not.toHaveBeenCalled();
    expect(mocks.client.POST).not.toHaveBeenCalled();
  });

  it('creates a channel with the selected provider', async () => {
    const user = userEvent.setup();
    mocks.client.POST.mockResolvedValue({ data: slackChannel });
    renderChannelsPage({});
    await user.click(screen.getByRole('button', { name: 'Add channel' }));
    await user.click(screen.getByLabelText('Provider'));
    await user.click(screen.getByRole('option', { name: 'Slack' }));
    const editor = within(screen.getByRole('dialog'));
    await user.clear(editor.getByLabelText('Channel name'));
    await user.paste('slack-test');
    await user.click(editor.getByLabelText('Slack webhook URL'));
    await user.paste('https://hooks.slack.com/services/test');
    await user.click(editor.getByRole('button', { name: 'Create channel' }));
    expect(mocks.client.POST).toHaveBeenCalledWith(
      '/notification-channels',
      expect.objectContaining({
        body: expect.objectContaining({
          type: 'slack',
          name: 'slack-test',
          slack: expect.objectContaining({
            webhookUrl: 'https://hooks.slack.com/services/test',
          }),
        }),
      })
    );
    expect(
      await screen.findByRole('listitem', { name: 'slack-test' })
    ).toBeVisible();
  });

  it('keeps edits and reports save failures inside the editor', async () => {
    const user = userEvent.setup();
    mocks.client.PUT.mockResolvedValue({ error: { message: 'Save failed' } });
    renderChannelsPage({}, [slackChannel]);
    await user.click(screen.getByRole('button', { name: 'Edit' }));
    await user.type(screen.getByLabelText('Channel name'), '-new');
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(
      within(screen.getByRole('dialog')).getByRole('alert')
    ).toHaveTextContent('Save failed');
    expect(screen.getByLabelText('Channel name')).toHaveValue('slack-test-new');
  });

  it('persists toggles and keeps the saved state when a toggle fails', async () => {
    const user = userEvent.setup();
    mocks.client.PUT.mockResolvedValueOnce({
      data: { ...slackChannel, enabled: false },
    }).mockResolvedValueOnce({ error: { message: 'Toggle failed' } });
    renderChannelsPage({}, [slackChannel]);
    const toggle = screen.getByRole('switch', { name: 'Toggle slack-test' });
    await user.click(toggle);
    expect(toggle).not.toBeChecked();
    await user.click(toggle);
    expect(toggle).not.toBeChecked();
    expect(screen.getByRole('alert')).toHaveTextContent('Toggle failed');
  });

  it('tests a disabled channel and shows delivery feedback', async () => {
    const user = userEvent.setup();
    mocks.client.POST.mockResolvedValueOnce({
      data: { results: [{ delivered: true }] },
    }).mockResolvedValueOnce({
      data: {
        results: [{ delivered: false, error: 'Destination unavailable' }],
      },
    });
    renderChannelsPage({}, [{ ...slackChannel, enabled: false }]);
    await user.click(screen.getByRole('button', { name: 'Test' }));
    expect(screen.getByText('Test delivered')).toBeVisible();
    expect(mocks.client.POST).toHaveBeenCalledWith(
      '/notification-channels/{channelId}/test',
      {
        params: {
          path: { channelId: 'slack' },
          query: { remoteNode: 'local' },
        },
      }
    );
    await user.click(screen.getByRole('button', { name: 'Test' }));
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Destination unavailable'
    );
  });

  it('deletes only the selected channel after confirmation', async () => {
    const user = userEvent.setup();
    mocks.client.DELETE.mockResolvedValue({});
    renderChannelsPage({}, [slackChannel]);
    await user.click(
      screen.getByRole('button', { name: 'Channel actions for slack-test' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Delete channel' }));
    await user.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: 'Delete',
      })
    );
    expect(mocks.client.DELETE).toHaveBeenCalledWith(
      '/notification-channels/{channelId}',
      {
        params: {
          path: { channelId: 'slack' },
          query: { remoteNode: 'local' },
        },
      }
    );
    expect(screen.getByText('No channels configured.')).toBeVisible();
  });

  it('cancels email delivery edits and saves only the configured sender', async () => {
    const user = userEvent.setup();
    const settings = {
      smtp: { host: 'smtp.example.com', port: '587', passwordConfigured: true },
    };
    mocks.client.PUT.mockResolvedValue({ data: settings });
    renderChannelsPage(settings);
    await user.click(screen.getByRole('button', { name: 'Configure' }));
    await user.clear(screen.getByPlaceholderText('SMTP host'));
    await user.type(
      screen.getByPlaceholderText('SMTP host'),
      'unsaved.example.com'
    );
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await user.click(screen.getByRole('button', { name: 'Configure' }));
    expect(screen.getByPlaceholderText('SMTP host')).toHaveValue(
      'smtp.example.com'
    );
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(mocks.client.PUT).toHaveBeenCalledWith(
      '/notification-settings',
      expect.objectContaining({
        body: {
          smtp: expect.objectContaining({
            host: 'smtp.example.com',
            password: undefined,
          }),
        },
      })
    );
    expect(screen.getByText('Email delivery saved')).toBeVisible();
  });

  it('blocks channel controls when delivery is unavailable', () => {
    mocks.useQuery.mockReturnValue({
      data: undefined,
      error: new Error('Notification delivery is unavailable'),
      isLoading: false,
      mutate: vi.fn(),
    });

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={{ setTitle: vi.fn() } as never}>
          <NotificationChannelsPage />
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.getByText('Notification delivery is unavailable')
    ).toBeVisible();
    expect(
      screen.queryByRole('button', { name: /^save$/i })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /add channel/i })
    ).not.toBeInTheDocument();
  });

  it('preserves the configured password indicator when toggling authentication modes', async () => {
    const user = userEvent.setup();
    renderChannelsPage({
      smtp: {
        host: 'smtp.example.com',
        port: '587',
        username: 'sender@example.com',
        passwordConfigured: true,
      },
    });

    await user.click(screen.getByRole('button', { name: 'Configure' }));
    expect(screen.getByPlaceholderText('Password configured')).toBeVisible();

    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'OAuth 2.0' }));
    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'Password' }));

    expect(screen.getByPlaceholderText('Password configured')).toBeVisible();
  });

  it('preserves configured OAuth indicators when toggling authentication modes', async () => {
    const user = userEvent.setup();
    renderChannelsPage({
      smtp: {
        host: 'smtp.office365.com',
        port: '587',
        username: 'sender@example.com',
        oauth: {
          provider: 'microsoft',
          tenantId: 'tenant',
          clientId: 'client',
          clientSecretConfigured: true,
          refreshTokenConfigured: false,
          serviceAccountJsonConfigured: false,
        },
      },
    });

    await user.click(screen.getByRole('button', { name: 'Configure' }));
    expect(
      screen.getByPlaceholderText('Client secret configured')
    ).toBeVisible();

    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'Password' }));
    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'OAuth 2.0' }));

    expect(
      screen.getByPlaceholderText('Client secret configured')
    ).toBeVisible();
  });

  it('keeps typed OAuth secrets when identity fields change', async () => {
    const user = userEvent.setup();
    renderChannelsPage({});
    await user.click(screen.getByRole('button', { name: 'Configure' }));

    await user.click(screen.getByLabelText('SMTP authentication'));
    await user.click(screen.getByRole('option', { name: 'OAuth 2.0' }));

    const clientSecret = screen.getByPlaceholderText('Client secret');
    await user.type(clientSecret, 'typed-secret');
    await user.type(
      screen.getByPlaceholderText('Microsoft tenant ID'),
      'tenant'
    );
    await user.type(screen.getByPlaceholderText('Client ID'), 'client');
    await user.type(
      screen.getByPlaceholderText('Sender mailbox'),
      'sender@example.com'
    );

    expect(clientSecret).toHaveValue('typed-secret');
  });
});

describe('NotificationRulesPage', () => {
  it.each(['remote node', 'workspace'])(
    'keeps pending rule saves on their original %s',
    async (scope) => {
      const user = userEvent.setup();
      let completeSave!: () => void;
      mocks.client.PUT.mockImplementationOnce(
        (_path: string, { body }: { body: object }) =>
          new Promise((resolve) => {
            completeSave = () => resolve({ data: body });
          })
      );
      const { changeScope } = renderRulesPage(
        [],
        [NotificationEventType.dag_run_failed],
        'ops'
      );
      if (scope === 'workspace') {
        await user.click(screen.getByLabelText('Applies to'));
        await user.click(screen.getByRole('option', { name: 'ops workspace' }));
        await user.click(
          screen.getByRole('button', { name: 'Configure workspace' })
        );
      }
      await user.click(screen.getByRole('checkbox', { name: 'Succeeded' }));
      await user.click(screen.getByRole('button', { name: 'Save changes' }));
      changeScope(
        scope === 'workspace' ? 'other' : 'ops',
        scope === 'remote node' ? 'remote' : 'local'
      );
      await act(async () => {
        completeSave();
      });
      if (scope === 'workspace') {
        await user.click(screen.getByLabelText('Applies to'));
        await user.click(
          screen.getByRole('option', { name: 'other workspace' })
        );
      }
      expect(screen.getByRole('checkbox', { name: 'Failed' })).toBeChecked();
      expect(
        screen.getByRole('checkbox', { name: 'Succeeded' })
      ).not.toBeChecked();
    }
  );

  it('loads operational defaults for saved routes without events', () => {
    renderRulesPage([], []);

    for (const name of ['Failed', 'Aborted', 'Rejected', 'Waiting']) {
      expect(screen.getByRole('checkbox', { name })).toBeChecked();
    }
    expect(
      screen.getByRole('checkbox', { name: 'Succeeded' })
    ).not.toBeChecked();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  });

  it('allows clearing events while editing and requires a selection to save', async () => {
    const user = userEvent.setup();
    renderRulesPage();

    await user.click(screen.getByRole('checkbox', { name: 'Aborted' }));
    await user.click(screen.getByRole('checkbox', { name: 'Rejected' }));

    for (const checkbox of screen.getAllByRole('checkbox')) {
      expect(checkbox).not.toBeChecked();
    }
    expect(
      screen.getByText('Select at least one event before saving.')
    ).toBeVisible();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();

    await user.click(screen.getByRole('checkbox', { name: 'Failed' }));
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled();
  });

  it('offers channel creation when every destination has a rule', async () => {
    const user = userEvent.setup();
    renderRulesPage();
    await user.click(screen.getByRole('button', { name: 'Add rule' }));
    const dialog = within(screen.getByRole('dialog'));
    expect(
      dialog.getByText(
        'Every channel already has a rule. Edit an existing rule or create another channel.'
      )
    ).toBeVisible();
    expect(dialog.getByRole('link', { name: 'Add channel' })).toHaveAttribute(
      'href',
      '/notification-channels'
    );
  });

  it('adds a route for an unused channel', async () => {
    const user = userEvent.setup();
    renderRulesPage([
      { id: 'email', name: 'email-test', type: 'email', enabled: true },
    ]);

    await user.click(screen.getByRole('button', { name: 'Add rule' }));
    await user.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: /email-test/,
      })
    );

    expect(
      screen.getByRole('switch', { name: 'Toggle email-test' })
    ).toBeChecked();
    expect(
      screen.getAllByRole('checkbox', { name: 'Failed' })[1]
    ).toBeChecked();
  });

  it('edits events through checkboxes and labels and saves them', async () => {
    const user = userEvent.setup();
    renderRulesPage();

    const failed = screen.getByRole('checkbox', {
      name: 'Failed',
    });
    const rejected = screen.getByRole('checkbox', {
      name: 'Rejected',
    });
    expect(failed).not.toBeChecked();
    expect(rejected).toBeChecked();

    await user.click(failed);
    expect(failed).toBeChecked();
    await user.click(rejected.closest('label')!);
    expect(rejected).not.toBeChecked();

    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(mocks.client.PUT).toHaveBeenCalledWith(
      '/notification-routes/global',
      {
        params: { query: { remoteNode: 'local' } },
        body: {
          enabled: true,
          inheritGlobal: true,
          routes: [
            {
              id: 'route',
              channelId: 'slack',
              enabled: true,
              events: [
                NotificationEventType.dag_run_aborted,
                NotificationEventType.dag_run_failed,
              ],
            },
          ],
        },
      }
    );
  });
  it('cancels edits without saving and clears the dirty state', async () => {
    const user = userEvent.setup();
    renderRulesPage();
    await user.click(screen.getByRole('checkbox', { name: 'Failed' }));
    expect(screen.getByText('Unsaved changes')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByRole('checkbox', { name: 'Failed' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Rejected' })).toBeChecked();
    expect(screen.getByText('All changes saved')).toBeVisible();
    expect(mocks.client.PUT).not.toHaveBeenCalled();
  });

  it('deletes a rule through its menu and can undo the deletion', async () => {
    const user = userEvent.setup();
    renderRulesPage();
    await user.click(
      screen.getByRole('button', { name: 'Rule actions for slack-test' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Delete rule' }));
    expect(screen.getByText('No notification rules yet')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(
      screen.getByRole('switch', { name: 'Toggle slack-test' })
    ).toBeChecked();
  });

  it('retains edits when saving fails', async () => {
    const user = userEvent.setup();
    mocks.client.PUT.mockResolvedValue({ error: { message: 'Save failed' } });
    renderRulesPage();
    await user.click(screen.getByRole('checkbox', { name: 'Failed' }));
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(screen.getByText('Save failed')).toBeVisible();
    expect(screen.getByText('Unsaved changes')).toBeVisible();
    expect(screen.getByRole('checkbox', { name: 'Failed' })).toBeChecked();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled();
  });

  it('tests the destination without saving rule edits', async () => {
    const user = userEvent.setup();
    mocks.client.POST.mockResolvedValue({
      data: { results: [{ delivered: true }] },
    });
    renderRulesPage();
    await user.click(screen.getByRole('checkbox', { name: 'Failed' }));
    await user.click(screen.getByRole('button', { name: 'Test channel' }));
    expect(screen.getByText('Test delivered')).toBeVisible();
    expect(mocks.client.POST).toHaveBeenCalledWith(
      '/notification-channels/{channelId}/test',
      {
        params: {
          path: { channelId: 'slack' },
          query: { remoteNode: 'local' },
        },
      }
    );
    expect(mocks.client.PUT).not.toHaveBeenCalled();
    expect(screen.getByText('Unsaved changes')).toBeVisible();
  });

  it('shows delivery failures', async () => {
    const user = userEvent.setup();
    mocks.client.POST.mockResolvedValue({
      data: {
        results: [{ delivered: false, error: 'Destination unavailable' }],
      },
    });
    renderRulesPage();
    await user.click(screen.getByRole('button', { name: 'Test channel' }));
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Destination unavailable'
    );
  });

  it('configures workspace overrides independently and restores inheritance', async () => {
    const user = userEvent.setup();
    renderRulesPage([], [NotificationEventType.dag_run_failed], 'ops');
    await user.click(screen.getByLabelText('Applies to'));
    await user.click(screen.getByRole('option', { name: 'ops workspace' }));
    expect(screen.getByText('Inheriting Global rules')).toBeVisible();
    expect(screen.getByRole('checkbox', { name: 'Failed' })).toBeDisabled();
    await user.click(
      screen.getByRole('button', { name: 'Configure workspace' })
    );
    await user.click(screen.getByRole('checkbox', { name: 'Succeeded' }));
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(mocks.client.PUT).toHaveBeenLastCalledWith(
      '/notification-routes/workspaces/{workspaceName}',
      expect.objectContaining({
        params: {
          path: { workspaceName: 'ops' },
          query: { remoteNode: 'local' },
        },
        body: expect.objectContaining({
          inheritGlobal: false,
          routes: [
            expect.objectContaining({
              events: ['dag.run.failed', 'dag.run.succeeded'],
            }),
          ],
        }),
      })
    );
    expect(screen.getByText('All changes saved')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Use Global rules' }));
    expect(
      screen.getByRole('checkbox', { name: 'Succeeded' })
    ).not.toBeChecked();
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(mocks.client.PUT).toHaveBeenLastCalledWith(
      '/notification-routes/workspaces/{workspaceName}',
      expect.objectContaining({
        body: expect.objectContaining({ inheritGlobal: true }),
      })
    );
  });
});
