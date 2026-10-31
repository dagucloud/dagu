// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RuntimeProfileEntryKind, RuntimeProfileStatus } from '@/api/v1/schema';
import { RuntimeProfileSection } from '../RuntimeProfileSection';

const state = vi.hoisted(() => ({
  canManage: true,
  isAdmin: true,
  settings: { profile: 'prod' } as { profile?: string },
  profiles: [] as unknown[],
  globalLayer: undefined as unknown,
  globalError: undefined as unknown,
  workspaceLayer: undefined as unknown,
  workspaceError: undefined as unknown,
  guardLeave: vi.fn(),
  setDirty: vi.fn(),
  dirtyCallback: undefined as undefined | ((dirty: boolean) => void),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanManageProfiles: () => state.canManage,
  useIsAdmin: () => state.isAdmin,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    if (init === null) return { data: undefined };
    if (path === '/dags/{fileName}/settings') return { data: state.settings };
    if (path === '/profiles/_global') {
      return { data: state.globalLayer, error: state.globalError };
    }
    if (path === '/profiles/_workspaces/{workspaceName}') {
      return { data: state.workspaceLayer, error: state.workspaceError };
    }
    return { data: { profiles: state.profiles }, isLoading: false };
  },
}));
vi.mock('../../settings/useSettingsLeaveGuard', () => ({
  useSettingsLeaveGuard: () => ({
    setDirty: state.setDirty,
    guardLeave: state.guardLeave,
  }),
}));
// The control that picks the profile has its own tests.
vi.mock('@/features/dags/components/dag-details/DAGSettingsTab', () => ({
  default: ({ onDirtyChange }: { onDirtyChange?: (d: boolean) => void }) => {
    state.dirtyCallback = onDirtyChange;
    return <div data-testid="settings-tab" />;
  },
}));

const at = '2026-10-01T10:20:00Z';

function entry(key: string, value: string) {
  return {
    key,
    kind: RuntimeProfileEntryKind.variable,
    value,
    createdAt: at,
    updatedAt: at,
  };
}

function secret(key: string) {
  return {
    key,
    kind: RuntimeProfileEntryKind.secret,
    secretId: `id-${key}`,
    createdAt: at,
    updatedAt: at,
  };
}

function profile(over: Record<string, unknown> = {}) {
  return {
    id: 'p-1',
    name: 'prod',
    description: 'Production settings',
    status: RuntimeProfileStatus.active,
    protected: false,
    entries: [entry('REGION', 'eu'), entry('TZ', 'JST'), secret('API_KEY')],
    createdAt: at,
    updatedAt: at,
    ...over,
  };
}

function layer(entries: unknown[], extra: Record<string, unknown> = {}) {
  return { name: 'layer', status: 'active', entries, ...extra };
}

function renderSection(workspaceName: string | null = 'ops') {
  return render(
    <MemoryRouter>
      <RuntimeProfileSection
        fileName="etl.yaml"
        workspaceName={workspaceName}
      />
    </MemoryRouter>
  );
}

const row = (key: string) => screen.getByTestId(`ww-rp-row-${key}`);

beforeEach(() => {
  state.canManage = true;
  state.isAdmin = true;
  state.settings = { profile: 'prod' };
  state.profiles = [profile()];
  state.globalLayer = layer([entry('TZ', 'UTC'), entry('LANG', 'C')]);
  state.globalError = undefined;
  state.workspaceLayer = layer([entry('REGION', 'us')], {});
  state.workspaceError = undefined;
  state.guardLeave.mockReset().mockImplementation((next: () => void) => next());
  state.setDirty.mockReset();
  state.dirtyCallback = undefined;
});

// parity:settings-default-profile
describe('RuntimeProfileSection', () => {
  it('names the profile of the workflow, with its facts', () => {
    renderSection();
    expect(
      screen.getByRole('region', { name: 'Runtime profile' })
    ).toBeInTheDocument();
    expect(screen.getByTestId('ww-rp-applies')).toHaveTextContent(
      'Yes: the default profile of this workflow'
    );
    expect(screen.getByTestId('ww-rp-name')).toHaveTextContent('prod');
    expect(screen.getByTestId('ww-rp-description')).toHaveTextContent(
      'Production settings'
    );
    expect(screen.getByTestId('ww-rp-counts')).toHaveTextContent(
      '3 variables, 1 secrets'
    );
    expect(screen.getByTestId('ww-rp-updated')).toHaveTextContent('2026-10-01');
    expect(screen.getByTestId('settings-tab')).toBeInTheDocument();
  });

  // Each layer is a .env block: its keys are the keys that it gives.
  it('shows one .env block for each layer, with what a key overrides', () => {
    renderSection();
    const groups = screen
      .getByTestId('ww-rp-list')
      .querySelectorAll('[role="group"]');
    expect(Array.from(groups).map((g) => g.getAttribute('aria-label'))).toEqual(
      ['Global environment', 'prod']
    );
    const global = within(screen.getByTestId('ww-rp-group-global'));
    expect(
      global.getAllByRole('listitem').map((r) => r.getAttribute('data-testid'))
    ).toEqual(['ww-rp-row-LANG']);
    expect(row('LANG')).toHaveTextContent('LANG=C');
    expect(screen.queryByTestId('ww-rp-group-workspace')).toBeNull();
    const profileBlock = within(screen.getByTestId('ww-rp-group-profile'));
    expect(
      profileBlock
        .getAllByRole('listitem')
        .map((r) => r.getAttribute('data-testid'))
    ).toEqual(['ww-rp-row-API_KEY', 'ww-rp-row-REGION', 'ww-rp-row-TZ']);
    expect(row('REGION')).toHaveTextContent('REGION=eu');
    expect(
      within(row('REGION')).getByText('overrides Workspace defaults')
    ).toBeVisible();
    expect(row('TZ')).toHaveTextContent('TZ=JST');
    expect(
      within(row('TZ')).getByText('overrides Global environment')
    ).toBeVisible();
  });

  // The API sends no secret value, and the block must not invent one.
  it('masks the value of a secret', () => {
    renderSection();
    const secretRow = within(row('API_KEY'));
    expect(secretRow.getByLabelText('Secret value hidden')).toHaveTextContent(
      '******'
    );
    expect(row('API_KEY')).toHaveTextContent('API_KEY=******');
    expect(secretRow.getByText('Secret')).toBeVisible();
    expect(secretRow.queryByText('id-API_KEY')).toBeNull();
  });

  it('shows 8 variables of a long layer and reveals the rest on request', () => {
    state.profiles = [
      profile({
        entries: Array.from({ length: 12 }, (_, i) =>
          entry(`VAR_${String(i + 1).padStart(2, '0')}`, `v${i + 1}`)
        ),
      }),
    ];
    renderSection();
    const block = within(screen.getByTestId('ww-rp-group-profile'));
    expect(block.getAllByRole('listitem')).toHaveLength(8);
    const more = block.getByRole('button', { name: 'Show 4 more' });
    expect(more).toHaveAttribute('aria-expanded', 'false');
    // The short global block has no button.
    expect(
      within(screen.getByTestId('ww-rp-group-global')).queryByRole('button')
    ).toBeNull();

    fireEvent.click(more);
    expect(block.getAllByRole('listitem')).toHaveLength(12);
    const less = block.getByRole('button', { name: 'Show less' });
    expect(less).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(less);
    expect(block.getAllByRole('listitem')).toHaveLength(8);
  });

  it('uses the default profile of the workspace when the workflow has none', () => {
    state.settings = {};
    state.workspaceLayer = layer([entry('REGION', 'us')], {
      defaultProfile: 'prod',
    });
    renderSection();
    expect(screen.getByTestId('ww-rp-applies')).toHaveTextContent(
      'Yes: the default profile of the workspace ops'
    );
    expect(screen.getByTestId('ww-rp-name')).toHaveTextContent('prod');
  });

  it('says that no profile applies, and still lists the inherited keys', () => {
    state.settings = {};
    renderSection();
    expect(screen.getByTestId('ww-rp-applies')).toHaveTextContent(
      'No profile applies to this workflow'
    );
    expect(screen.getByTestId('ww-rp-name')).toHaveTextContent('No profile');
    expect(screen.queryByTestId('ww-rp-counts')).toBeNull();
    expect(screen.getByTestId('ww-rp-group-global')).toBeInTheDocument();
    expect(row('LANG')).toHaveTextContent('LANG=C');
    expect(screen.getByTestId('settings-tab')).toBeInTheDocument();
  });

  it('flags a disabled or protected profile', () => {
    state.profiles = [
      profile({ status: RuntimeProfileStatus.disabled, protected: true }),
    ];
    renderSection();
    expect(screen.getByTestId('ww-rp-name')).toHaveTextContent('Disabled');
    expect(screen.getByTestId('ww-rp-name')).toHaveTextContent('Protected');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'This profile is disabled. A run cannot use it.'
    );
  });

  // A viewer sees the name from the settings of the workflow, not the content.
  it('shows only the name to a role that cannot manage profiles', () => {
    state.canManage = false;
    state.isAdmin = false;
    state.profiles = [];
    state.globalLayer = undefined;
    state.workspaceLayer = undefined;
    renderSection();
    expect(screen.getByTestId('ww-rp-name')).toHaveTextContent('prod');
    expect(screen.queryByTestId('ww-rp-list')).toBeNull();
    expect(screen.queryByRole('link', { name: 'Manage profiles' })).toBeNull();
    expect(
      screen.getByText('Global variables are visible to administrators only.')
    ).toBeVisible();
    expect(
      screen.getByText(
        'The defaults of the workspace are not available to your role.'
      )
    ).toBeVisible();
    expect(screen.getByTestId('settings-tab')).toBeInTheDocument();
  });

  it('keeps the profile keys when a layer cannot be read', () => {
    state.globalLayer = undefined;
    state.globalError = new Error('forbidden');
    state.workspaceLayer = undefined;
    state.workspaceError = new Error('forbidden');
    renderSection();
    expect(row('TZ')).toHaveTextContent('TZ=JST');
    expect(screen.queryByTestId('ww-rp-row-LANG')).toBeNull();
    expect(
      screen.getByText('Global variables are visible to administrators only.')
    ).toBeVisible();
  });

  it('leaves through the guard to the profiles page', () => {
    renderSection();
    const link = screen.getByRole('link', { name: 'Manage profiles' });
    expect(link).toHaveAttribute('href', '/profiles');
    link.click();
    expect(state.guardLeave).toHaveBeenCalledTimes(1);
  });

  // An unsaved choice of the control is a draft of the leave guard; the draft
  // ends with the section.
  it('reports an unsaved choice to the leave guard and clears it on unmount', () => {
    const view = renderSection();
    // The section hands its callback to the control.
    state.dirtyCallback!(true);
    expect(state.setDirty).toHaveBeenCalledWith('profile', true);
    state.dirtyCallback!(false);
    expect(state.setDirty).toHaveBeenLastCalledWith('profile', false);
    state.dirtyCallback!(true);
    view.unmount();
    expect(state.setDirty).toHaveBeenLastCalledWith('profile', false);
  });
});
