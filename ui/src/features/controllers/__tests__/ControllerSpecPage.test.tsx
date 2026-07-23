// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { Status, StatusLabel } from '@/api/v1/schema';
import ControllerSpecPage from '@/pages/controllers/controller/spec';
import { createControllerDraft, serializeControllerDefinition } from '../draft';
import type { ControllerDetail } from '../types';

const fakes = vi.hoisted(() => ({
  canWrite: vi.fn(() => true),
  create: vi.fn(),
  delete: vi.fn(),
  details: new Map<string, ControllerDetail>(),
  detailMutate: vi.fn(),
  navigate: vi.fn(),
  routeNavigate: null as ((to: string) => void) | null,
  showToast: vi.fn(),
  update: vi.fn(),
}));

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => {
      const navigate = actual.useNavigate();
      return ((...args: unknown[]) => {
        fakes.navigate(...args);
        return (navigate as (...values: unknown[]) => void)(...args);
      }) as typeof navigate;
    },
  };
});

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => fakes.canWrite(),
}));

vi.mock('@/features/controllers/api', () => ({
  useControllerAPI: () => ({
    create: fakes.create,
    delete: fakes.delete,
    update: fakes.update,
  }),
  useControllerDAGOptions: () => ({
    data: [],
    error: undefined,
    isLoading: false,
    mutate: vi.fn(),
  }),
  useControllerDetail: (id: string | undefined) => ({
    data: id ? fakes.details.get(id) : undefined,
    error: undefined,
    isLoading: false,
    mutate: fakes.detailMutate,
  }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: fakes.showToast }),
}));

vi.mock('@/features/dags/components/dag-editor/DAGEditorWithDocs', () => ({
  default: () => null,
}));

function validCreateSpec(): string {
  const definition = createControllerDraft('ops');
  definition.name = 'Incident router';
  definition.description = 'Route incident work.';
  definition.llm.model = 'gpt-5';
  definition.states.default = {
    description: 'Finish the requested work.',
    dags: [],
    transitions: [],
    terminal: 'succeeded',
  };
  return serializeControllerDefinition(definition);
}

function persistedDetail(id: string, name: string): ControllerDetail {
  const definition = createControllerDraft('ops');
  definition.id = id;
  definition.name = name;
  definition.description = 'Route incident work.';
  definition.llm.model = 'gpt-5';
  definition.states.default = {
    description: 'Finish the requested work.',
    dags: [],
    transitions: [],
    terminal: 'succeeded',
  };
  return {
    id,
    definition: definition as ControllerDetail['definition'],
    runtime: {
      status: Status.NotStarted,
      statusLabel: StatusLabel.not_started,
      currentState: '',
      turnCount: 0,
      dagRunRefs: [],
      context: [],
    },
    dagRuns: [],
    spec: serializeControllerDefinition(definition),
    warnings: [],
    resourceUpdatedAt: '2026-01-01T00:00:00Z',
  };
}

function NavigationProbe() {
  const navigate = useNavigate();
  const location = useLocation();
  fakes.routeNavigate = (to) => navigate(to);
  return <output aria-label="Current route">{location.pathname}</output>;
}

function renderPage(duplicateSpec?: string) {
  return render(
    <MemoryRouter
      initialEntries={[
        {
          pathname: '/controllers/new/spec',
          state: { workspace: 'ops', duplicateSpec },
        },
      ]}
    >
      <Routes>
        <Route
          path="/controllers/new/spec"
          element={<ControllerSpecPage isNew />}
        />
      </Routes>
    </MemoryRouter>
  );
}

function renderPersistedPage(id: string) {
  return render(
    <MemoryRouter initialEntries={[`/controllers/${id}/spec`]}>
      <NavigationProbe />
      <Routes>
        <Route path="/controllers/:id/spec" element={<ControllerSpecPage />} />
        <Route path="/controllers" element={<div>Controller list</div>} />
      </Routes>
    </MemoryRouter>
  );
}

describe('ControllerSpecPage', () => {
  beforeEach(() => {
    fakes.canWrite.mockReset();
    fakes.canWrite.mockReturnValue(true);
    fakes.create.mockReset();
    fakes.delete.mockReset();
    fakes.details.clear();
    fakes.detailMutate.mockReset();
    fakes.detailMutate.mockImplementation(async (value) => value);
    fakes.navigate.mockReset();
    fakes.routeNavigate = null;
    fakes.showToast.mockReset();
    fakes.update.mockReset();
  });

  it('keeps a new draft editable when its target workspace is not writable', () => {
    fakes.canWrite.mockReturnValue(false);
    renderPage();

    expect(
      screen.getByRole('textbox', { name: 'Controller labels' })
    ).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(screen.getByText('Save unavailable')).toBeInTheDocument();
  });

  it.each([
    ['default draft', undefined],
    ['duplicate draft', validCreateSpec()],
  ])(
    'does not mark an untouched %s as unsaved',
    async (_name, duplicateSpec) => {
      const user = userEvent.setup();
      const confirm = vi.spyOn(window, 'confirm');
      renderPage(duplicateSpec);

      expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument();
      if (duplicateSpec) {
        expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
      }
      await user.click(screen.getByRole('link', { name: 'Cancel' }));
      expect(confirm).not.toHaveBeenCalled();
      confirm.mockRestore();
    }
  );

  it('shows structured warnings from the persisted Controller detail', () => {
    const id = 'ctrl_aaaaaaaaaaaaaaaa';
    const detail = persistedDetail(id, 'Incident Controller');
    detail.warnings = [
      {
        code: 'unreachable_state',
        path: 'states.review',
        message: 'State is unreachable from default.',
      },
      {
        code: 'unused_dag',
        path: 'dags[0]',
        message: 'DAG is not referenced by any State.',
      },
    ];
    fakes.details.set(id, detail);

    renderPersistedPage(id);

    expect(screen.getByText('Definition warnings')).toBeVisible();
    expect(
      screen.getByText(
        (_, element) =>
          element?.tagName === 'LI' &&
          element.textContent ===
            'states.review: State is unreachable from default.'
      )
    ).toBeVisible();
    expect(
      screen.getByText(
        (_, element) =>
          element?.tagName === 'LI' &&
          element.textContent === 'dags[0]: DAG is not referenced by any State.'
      )
    ).toBeVisible();
  });

  it('blocks Cancel during creation and does not redirect after unmount', async () => {
    const user = userEvent.setup();
    let resolveCreate!: (value: { id: string }) => void;
    fakes.create.mockImplementation(
      () =>
        new Promise<{ id: string }>((resolve) => {
          resolveCreate = resolve;
        })
    );
    const view = renderPage(validCreateSpec());

    await user.click(screen.getByRole('button', { name: 'Save' }));

    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();

    view.unmount();
    await act(async () => {
      resolveCreate({ id: 'ctrl_aaaaaaaaaaaaaaaa' });
      await Promise.resolve();
    });

    expect(fakes.navigate).not.toHaveBeenCalled();
  });

  it('does not apply a late update response to a different Controller route', async () => {
    const user = userEvent.setup();
    const firstID = 'ctrl_aaaaaaaaaaaaaaaa';
    const secondID = 'ctrl_bbbbbbbbbbbbbbbb';
    const first = persistedDetail(firstID, 'First Controller');
    const second = persistedDetail(secondID, 'Second Controller');
    fakes.details.set(firstID, first);
    fakes.details.set(secondID, second);
    let resolveUpdate!: (detail: ControllerDetail) => void;
    fakes.update.mockImplementation(
      () =>
        new Promise<ControllerDetail>((resolve) => {
          resolveUpdate = resolve;
        })
    );
    renderPersistedPage(firstID);
    const name = await screen.findByDisplayValue('First Controller');
    await user.clear(name);
    await user.type(name, 'Updated First Controller');
    await user.click(screen.getByRole('button', { name: 'Save' }));

    act(() => fakes.routeNavigate?.(`/controllers/${secondID}/spec`));
    expect(await screen.findByDisplayValue('Second Controller')).toBeVisible();

    await act(async () => {
      resolveUpdate({
        ...first,
        definition: {
          ...first.definition,
          name: 'Updated First Controller',
        },
        spec: first.spec.replace(
          'name: First Controller',
          'name: Updated First Controller'
        ),
      });
      await Promise.resolve();
    });

    expect(screen.getByDisplayValue('Second Controller')).toBeVisible();
    expect(
      screen.queryByDisplayValue('Updated First Controller')
    ).not.toBeInTheDocument();
  });

  it('does not redirect after a pending delete leaves its originating route', async () => {
    const user = userEvent.setup();
    const firstID = 'ctrl_aaaaaaaaaaaaaaaa';
    const secondID = 'ctrl_bbbbbbbbbbbbbbbb';
    fakes.details.set(firstID, persistedDetail(firstID, 'First Controller'));
    fakes.details.set(secondID, persistedDetail(secondID, 'Second Controller'));
    let resolveDelete!: () => void;
    fakes.delete.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveDelete = resolve;
        })
    );
    renderPersistedPage(firstID);
    await screen.findByDisplayValue('First Controller');
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: 'Delete',
      })
    );

    act(() => fakes.routeNavigate?.(`/controllers/${secondID}/spec`));
    expect(await screen.findByDisplayValue('Second Controller')).toBeVisible();

    await act(async () => {
      resolveDelete();
      await Promise.resolve();
    });

    expect(screen.getByLabelText('Current route')).toHaveTextContent(
      `/controllers/${secondID}/spec`
    );
    expect(screen.getByDisplayValue('Second Controller')).toBeVisible();
  });
});
