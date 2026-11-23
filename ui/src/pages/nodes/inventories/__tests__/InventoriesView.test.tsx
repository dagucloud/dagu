// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { InventoriesView } from '../InventoriesView';

const client = vi.hoisted(() => ({
  GET: vi.fn(),
  POST: vi.fn(),
  PUT: vi.fn(),
  DELETE: vi.fn(),
}));
const editor = vi.hoisted(() => ({
  setDiagnosticsOptions: vi.fn(),
  onValidate: null as null | ((markers: unknown[]) => void),
}));

vi.mock('@/hooks/api', () => ({ useClient: () => client }));
vi.mock('monaco-editor', () => ({
  languages: {
    json: {
      jsonDefaults: { setDiagnosticsOptions: editor.setDiagnosticsOptions },
    },
  },
  MarkerSeverity: { Error: 8, Warning: 4 },
}));
vi.mock('@monaco-editor/react', () => ({
  loader: { config: vi.fn() },
  // A textarea stands in for the editor; onValidate is kept for the test.
  default: (props: {
    value: string;
    onChange: (value: string) => void;
    onValidate: (markers: unknown[]) => void;
    options: { ariaLabel: string };
  }) => {
    editor.onValidate = props.onValidate;
    return (
      <textarea
        aria-label={props.options.ariaLabel}
        value={props.value}
        onChange={(event) => props.onChange(event.target.value)}
      />
    );
  },
}));

type Inventory = components['schemas']['Inventory'];

const TOKYO: Inventory = {
  id: 'inv_tokyo00001',
  name: 'Tokyo production',
  description: 'Racks A and B',
  version: 3,
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-02T00:00:00Z',
  defaults: {
    user: 'deploy',
    key: '~/.ssh/id_ed25519',
    bastion: 'tokyo-jump',
    shell: 'bash',
    labels: ['site=tokyo'],
  },
  nodes: [
    { name: 'tokyo-jump', host: 'jump.tokyo.example.com' },
    { name: 'tokyo-web01', host: '10.0.1.11', labels: ['web'] },
  ],
};

const SUMMARY = {
  id: TOKYO.id,
  name: TOKYO.name,
  description: TOKYO.description,
  nodeCount: 2,
  version: TOKYO.version,
  createdAt: TOKYO.createdAt,
  updatedAt: TOKYO.updatedAt,
};

function renderView(canEdit = true) {
  const onChanged = vi.fn();
  render(
    <InventoriesView
      inventories={[SUMMARY]}
      canEdit={canEdit}
      onChanged={onChanged}
    />
  );
  return onChanged;
}

function field(label: string) {
  return screen.getByLabelText(label);
}

describe('InventoriesView', () => {
  beforeEach(() => {
    for (const fn of Object.values(client)) {
      fn.mockReset();
    }
    client.GET.mockResolvedValue({ data: TOKYO });
    editor.setDiagnosticsOptions.mockReset();
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          new Response(JSON.stringify({ type: 'object' }), { status: 200 })
        )
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('lists inventories with their node counts, and offers no change without the role', () => {
    renderView(false);

    const row = screen.getByText('Tokyo production').closest('tr')!;
    expect(row).toHaveTextContent('Racks A and B');
    expect(row).toHaveTextContent('2');
    expect(
      screen.queryByRole('button', { name: 'New inventory' })
    ).not.toBeInTheDocument();
    expect(within(row).queryAllByRole('button')).toHaveLength(0);
  });

  it('creates an inventory from its name and defaults', async () => {
    client.POST.mockResolvedValue({ data: TOKYO });
    const onChanged = renderView();

    fireEvent.click(screen.getByRole('button', { name: 'New inventory' }));
    fireEvent.change(field('Name'), { target: { value: ' Osaka staging ' } });
    fireEvent.change(field('Default user'), { target: { value: 'deploy' } });
    fireEvent.change(field('Default port'), { target: { value: '2222' } });
    fireEvent.change(field('Key secret'), { target: { value: 'ssh/osaka' } });
    fireEvent.change(field('Default labels'), {
      target: { value: 'site=osaka, env=stg,' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    });

    expect(client.POST).toHaveBeenCalledWith('/inventories', {
      body: {
        name: 'Osaka staging',
        defaults: {
          user: 'deploy',
          port: 2222,
          key: { secret: 'ssh/osaka' },
          labels: ['site=osaka', 'env=stg'],
        },
        nodes: [],
      },
    });
    expect(onChanged).toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('renames an inventory and changes a default, keeping what the form does not show', async () => {
    client.PUT.mockResolvedValue({ data: TOKYO });
    renderView();

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Edit Tokyo production' })
      );
    });
    expect(field('Key file')).toHaveValue('~/.ssh/id_ed25519');
    fireEvent.change(field('Name'), { target: { value: 'Tokyo' } });
    fireEvent.change(field('Default user'), { target: { value: 'ops' } });
    fireEvent.click(
      screen.getByRole('checkbox', {
        name: 'Refuse host keys that are neither pinned nor trusted',
      })
    );
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    });

    expect(client.PUT).toHaveBeenCalledWith('/inventories/{inventoryId}', {
      params: { path: { inventoryId: TOKYO.id }, query: { version: 3 } },
      body: {
        name: 'Tokyo',
        description: 'Racks A and B',
        defaults: {
          user: 'ops',
          key: '~/.ssh/id_ed25519',
          bastion: 'tokyo-jump',
          shell: 'bash',
          labels: ['site=tokyo'],
          strict_host_key: false,
        },
        nodes: TOKYO.nodes,
      },
    });
  });

  it('refuses a key file and a key secret together', async () => {
    renderView();

    fireEvent.click(screen.getByRole('button', { name: 'New inventory' }));
    fireEvent.change(field('Name'), { target: { value: 'Osaka' } });
    fireEvent.change(field('Key file'), { target: { value: '/k' } });
    fireEvent.change(field('Key secret'), { target: { value: 'ssh/k' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    });

    expect(screen.getByRole('alert')).toHaveTextContent(
      'Give a key file or a key secret, not both.'
    );
    expect(client.POST).not.toHaveBeenCalled();
  });

  it('lists each rule a refused change breaks', async () => {
    client.POST.mockResolvedValue({
      error: {
        code: 'bad_request',
        message: 'inventory "Osaka" breaks 1 rule',
        errors: [
          { inventory: 'Osaka', field: 'defaults.user', message: 'invalid' },
        ],
      },
    });
    renderView();

    fireEvent.click(screen.getByRole('button', { name: 'New inventory' }));
    fireEvent.change(field('Name'), { target: { value: 'Osaka' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    });

    expect(screen.getByRole('alert')).toHaveTextContent(
      'defaults.user: invalid'
    );
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('loads the current version after a change based on an older one', async () => {
    const current = { ...TOKYO, version: 4, defaults: { user: 'admin' } };
    client.PUT.mockResolvedValueOnce({
      error: {
        code: 'conflict',
        message: 'inventory changed',
        inventory: current,
      },
    }).mockResolvedValueOnce({ data: current });
    renderView();

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Edit Tokyo production' })
      );
    });
    fireEvent.change(field('Default user'), { target: { value: 'ops' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    });
    fireEvent.click(
      screen.getByRole('button', { name: 'Load current version' })
    );

    expect(field('Default user')).toHaveValue('admin');
    fireEvent.change(field('Default user'), { target: { value: 'ops' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    });
    expect(client.PUT.mock.calls[1]![1].params.query).toEqual({ version: 4 });
  });

  it('deletes an inventory at the version listed, after asking', async () => {
    client.DELETE.mockResolvedValue({});
    const onChanged = renderView();

    fireEvent.click(
      screen.getByRole('button', { name: 'Delete Tokyo production' })
    );
    const confirm = screen.getByRole('dialog');
    expect(confirm).toHaveTextContent('its 2 nodes');
    await act(async () => {
      fireEvent.click(within(confirm).getByRole('button', { name: 'Delete' }));
    });

    expect(client.DELETE).toHaveBeenCalledWith('/inventories/{inventoryId}', {
      params: { path: { inventoryId: TOKYO.id }, query: { version: 3 } },
    });
    expect(onChanged).toHaveBeenCalled();
  });

  it('edits an inventory as JSON, checked against the schema', async () => {
    client.PUT.mockResolvedValue({ data: TOKYO });
    renderView();

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Edit JSON Tokyo production' })
      );
    });
    const text = await screen.findByLabelText('Inventory JSON');
    const record = JSON.parse((text as HTMLTextAreaElement).value);
    expect(record).toEqual({
      name: TOKYO.name,
      description: TOKYO.description,
      defaults: TOKYO.defaults,
      nodes: TOKYO.nodes,
    });
    expect(editor.setDiagnosticsOptions).toHaveBeenCalledWith(
      expect.objectContaining({
        schemas: [expect.objectContaining({ schema: { type: 'object' } })],
      })
    );
    const save = screen.getByRole('button', { name: 'Save' });

    fireEvent.change(text, { target: { value: '{"name": ' } });
    expect(save).toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent(
      'The text is not valid JSON.'
    );

    fireEvent.change(text, {
      target: { value: JSON.stringify({ ...record, lables: [] }) },
    });
    act(() =>
      editor.onValidate!([
        {
          severity: 8,
          startLineNumber: 1,
          startColumn: 2,
          message: 'Property lables is not allowed.',
        },
      ])
    );
    expect(save).toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Property lables is not allowed.'
    );

    const renamed = { ...record, name: 'Tokyo' };
    fireEvent.change(text, { target: { value: JSON.stringify(renamed) } });
    act(() => editor.onValidate!([]));
    await act(async () => {
      fireEvent.click(save);
    });
    expect(client.PUT).toHaveBeenCalledWith('/inventories/{inventoryId}', {
      params: { path: { inventoryId: TOKYO.id }, query: { version: 3 } },
      body: renamed,
    });
  });
});
