// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { pickOption } from '@/features/ai-endpoints/__tests__/pickerHelpers';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { VisualCanvasProps } from '../../contracts';
import { catalogSample } from '../../testing/fixtures';
import { buildCanvasGraph } from '../graph';
import { VisualCanvas } from '../VisualCanvas';
import { CANVAS_DETAILS_KEY } from '../useCanvasDetails';
import { makeModel, makeNames, makeStep } from './fixtures';

// Spec 093, "The UI contract": the Model node of the canvas edits the YAML
// through the same operation as the form, for the DAG of the editor.

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => ({
    data:
      path === '/ai/endpoint-choices' && init !== null
        ? {
            choices: [
              {
                name: 'cle1',
                protocol: 'anthropic',
                preset: 'custom',
                scope: 'global',
                models: ['claude-opus-5-5'],
              },
              {
                name: 'cle2',
                protocol: 'anthropic',
                preset: 'custom',
                scope: 'global',
                models: [],
              },
            ],
            cliModelAliases: ['sonnet'],
          }
        : undefined,
  }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

afterEach(() => {
  window.localStorage.clear();
});

function agentGraph(
  action = 'agent.run',
  withValue: Record<string, unknown> = {
    provider: 'claude',
    endpoint: { name: 'cle1', model: 'claude-opus-5-5' },
    prompt: 'x',
  }
) {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      explicitName: 'code',
      kind: 'action',
      action,
      value: { action, with: withValue },
    }),
  ]);
  return buildCanvasGraph({
    model,
    names: makeNames(model),
    issues: { byStep: {}, dag: [] },
  });
}

function renderCanvas(overrides: Partial<VisualCanvasProps> = {}) {
  const props: VisualCanvasProps = {
    graph: agentGraph(),
    direction: 'TB',
    selected: null,
    frozen: false,
    readOnly: false,
    catalog: catalogSample,
    dag: { name: 'fix-bug' },
    onSelect: vi.fn(),
    onEdit: vi.fn(),
    onCommand: vi.fn(),
    onDirectionChange: vi.fn(),
    ...overrides,
  };
  const view = render(
    <UserPreferencesProvider>
      <VisualCanvas {...props} />
    </UserPreferencesProvider>
  );
  return { ...view, props };
}

describe('the Model node in the canvas', () => {
  it('writes the endpoint through onEdit, as the form does', () => {
    const { props } = renderCanvas();
    const card = screen.getByTestId('ve-canvas-node-toolsat:steps[0]:model');
    expect(card).toHaveAttribute('data-shape', 'model-card');
    pickOption(
      within(card).getByRole('combobox', { name: 'Endpoint' }),
      'cle2'
    );
    expect(props.onEdit).toHaveBeenCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['with', 'endpoint', 'name'],
      value: 'cle2',
    });
  });

  it('converts a chat step of the old method when it gets an endpoint, in the same edits as the form', () => {
    const { props } = renderCanvas({
      graph: agentGraph('chat.completion', {
        provider: 'openai',
        base_url: 'http://localhost:8080',
        model: 'claude-opus-5-5',
        prompt: 'x',
      }),
    });
    const card = screen.getByTestId('ve-canvas-node-toolsat:steps[0]:model');
    pickOption(
      within(card).getByRole('combobox', { name: 'Endpoint' }),
      'cle1'
    );
    const path = (key: string[], value: unknown) => ({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['with', ...key],
      value,
    });
    expect(vi.mocked(props.onEdit).mock.calls.map(([op]) => op)).toEqual([
      path(['endpoint', 'name'], 'cle1'),
      path(['endpoint', 'model'], 'claude-opus-5-5'),
      path(['provider'], undefined),
      path(['base_url'], undefined),
      path(['model'], undefined),
    ]);
  });

  it('writes nothing on a frozen canvas, and draws no card in a run view', () => {
    const frozen = renderCanvas({ frozen: true });
    const select = within(
      screen.getByTestId('ve-canvas-node-toolsat:steps[0]:model')
    ).getByRole('combobox', { name: 'Endpoint' });
    expect(select).toBeDisabled();
    expect(frozen.props.onEdit).not.toHaveBeenCalled();
    frozen.unmount();

    renderCanvas({ readOnly: true });
    expect(
      screen.getByTestId('ve-canvas-node-toolsat:steps[0]:model')
    ).toHaveAttribute('data-shape', 'model');
    expect(screen.queryByRole('combobox', { name: 'Endpoint' })).toBeNull();
  });

  it('draws the round node of text when the canvas has no details', () => {
    window.localStorage.setItem(CANVAS_DETAILS_KEY, '0');
    renderCanvas();
    expect(
      screen.getByTestId('ve-canvas-node-toolsat:steps[0]:model')
    ).toHaveAttribute('data-shape', 'model');
    expect(screen.queryByRole('combobox', { name: 'Endpoint' })).toBeNull();
  });
});
