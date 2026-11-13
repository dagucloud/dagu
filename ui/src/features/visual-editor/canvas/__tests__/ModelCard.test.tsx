// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import {
  optionNames,
  pickOption,
} from '@/features/ai-endpoints/__tests__/pickerHelpers';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CanvasNode, DagEditOp } from '../../contracts';
import { CanvasActionsContext, type CanvasActions } from '../context';
import { FlowProvider, type NodeProps } from '../engine';
import { buildCanvasGraph } from '../graph';
import { ModelSubNode } from '../nodes/ModelSubNode';
import { modelSatelliteKey, withToolSatellites } from '../toolSatellites';
import type { CanvasNodeWithRoutes, StepFlowNode } from '../types';
import { makeModel, makeNames, makeStep } from './fixtures';

// Spec 093, "The UI contract": the Model node of an edit view has an Endpoint
// select and a Model field, which write the YAML as the form does.

const mocks = vi.hoisted(() => ({
  choices: undefined as unknown,
  queries: [] as unknown[],
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    if (path === '/ai/endpoint-choices') mocks.queries.push(init);
    return {
      data:
        path === '/ai/endpoint-choices' && init !== null
          ? mocks.choices
          : undefined,
    };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

afterEach(() => {
  mocks.choices = undefined;
  mocks.queries = [];
});

const last = <T,>(items: T[]): T | undefined => items[items.length - 1];

const MODELS = ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5'];

const choice = (
  name: string,
  protocol: 'anthropic' | 'openai',
  models: string[] = []
) => ({ name, protocol, preset: 'custom', scope: 'global', models });

/** The route of choices of a server that makes the model check. */
const served = (...choices: ReturnType<typeof choice>[]) => ({
  choices,
  cliModelAliases: ['sonnet', 'opus', 'haiku', 'opusplan', 'default'],
});

const DAG = { name: 'fix-bug' };

function modelNode(
  action: 'agent.run' | 'chat.completion',
  withValue: Record<string, unknown>,
  controls = true
): CanvasNodeWithRoutes {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      explicitName: 'triage',
      kind: 'action',
      action,
      value: { action, with: withValue },
    }),
  ]);
  const graph = withToolSatellites(
    buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    }),
    true,
    controls
  );
  const node = graph.nodes.find(
    (item) => item.key === modelSatelliteKey('steps[0]')
  );
  if (!node) throw new Error('the step has no Model node');
  return node as CanvasNodeWithRoutes;
}

function props(node: CanvasNode): NodeProps<StepFlowNode> {
  return {
    id: node.key,
    data: { node },
    selected: false,
    isConnectable: false,
  };
}

function show(
  node: CanvasNodeWithRoutes,
  over: Partial<CanvasActions> = {}
): { edits: DagEditOp[]; ui: ReactElement } {
  const edits: DagEditOp[] = [];
  const actions: CanvasActions = {
    direction: 'TB',
    editable: true,
    dag: DAG,
    onEdit: (op) => edits.push(op),
    onCommand: vi.fn(),
    onDeleteStep: () => {},
    onInsertOnEdge: () => {},
    ...over,
  };
  const ui = (
    <FlowProvider>
      <CanvasActionsContext.Provider value={actions}>
        <ModelSubNode {...props(node)} />
      </CanvasActionsContext.Provider>
    </FlowProvider>
  );
  return { edits, ui };
}

function renderNode(
  node: CanvasNodeWithRoutes,
  over: Partial<CanvasActions> = {}
) {
  const shown = show(node, over);
  return { ...render(shown.ui), edits: shown.edits };
}

const setField = (path: (string | number)[], value: unknown): DagEditOp => ({
  kind: 'setStepField',
  step: 'steps[0]',
  path,
  value,
});

const endpointSelect = () => screen.getByRole('combobox', { name: 'Endpoint' });
const modelField = () => screen.getByLabelText('Model', { selector: 'input' });
const modelSelect = () => screen.getByRole('combobox', { name: 'Model' });

describe('the Model node of an agent step', () => {
  it('has an Endpoint select of the protocol anthropic, with no (none) item, a Model field and the CLI as a muted line', () => {
    mocks.choices = served(
      choice('cle1', 'anthropic', MODELS),
      choice('gpt', 'openai', ['gpt-5'])
    );
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'claude-haiku-5' },
      })
    );
    expect(
      screen.getByTestId('ve-canvas-node-toolsat:steps[0]:model')
    ).toHaveAttribute('data-shape', 'model-card');
    // The endpoint of an agent step is required: the list cannot clear it.
    expect(optionNames(endpointSelect())).toEqual(['cle1']);
    expect(endpointSelect()).toHaveTextContent('cle1');
    expect(modelSelect()).toHaveTextContent('claude-haiku-5');
    expect(screen.getByTestId('ve-model-cli')).toHaveTextContent('claude');
    // Nothing is wrong: the model is in the list.
    expect(screen.queryByTestId('ve-model-warning')).toBeNull();
  });

  it('asks the route of choices for its DAG, and writes the endpoint with the edit of the form', () => {
    mocks.choices = served(
      choice('cle1', 'anthropic', MODELS),
      choice('cle2', 'anthropic')
    );
    const { edits } = renderNode(
      modelNode('agent.run', { provider: 'claude', endpoint: { name: 'cle1' } })
    );
    expect(last(mocks.queries)).toEqual({
      params: { query: { remoteNode: 'local', dag: 'fix-bug' } },
    });
    pickOption(endpointSelect(), 'cle2');
    expect(edits).toEqual([setField(['with', 'endpoint', 'name'], 'cle2')]);
    // An agent step needs an endpoint: there is no item that removes the key.
    expect(optionNames(endpointSelect())).toEqual(['cle1', 'cle2']);
  });

  it('offers the models of the endpoint and the aliases of the CLI, and writes the pick', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    const { edits } = renderNode(
      modelNode('agent.run', { provider: 'claude', endpoint: { name: 'cle1' } })
    );
    expect(optionNames(modelSelect())).toEqual([
      ...MODELS,
      'sonnet',
      'opus',
      'haiku',
      'opusplan',
      'default',
    ]);
    pickOption(modelSelect(), 'claude-haiku-5');
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'model'], 'claude-haiku-5'),
    ]);
  });

  it('asks for an endpoint and for a model, and locks the effort, while an agent step has no endpoint', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    renderNode(modelNode('agent.run', { provider: 'claude' }));
    expect(endpointSelect()).toHaveTextContent('Select an endpoint');
    // An agent step is refused when the DAG loads: a red marker, and a red border.
    const mark = screen.getByTestId('ve-model-warning');
    expect(mark).toHaveAttribute(
      'title',
      'agent.run needs an endpoint; add one on the AI page and name it here'
    );
    expect(mark).toHaveClass('text-destructive');
    expect(mark).not.toHaveClass('text-warning');
    expect(endpointSelect()).toHaveClass('border-destructive');
    // No model is offered without an endpoint: the picker asks for one.
    expect(modelSelect()).toHaveTextContent('(choose a model)');
    expect(modelSelect()).toBeEnabled();
    // The empty picker offers nothing: "(choose a model)" is its text, not an item.
    open(modelSelect());
    const list = screen.getByRole('listbox');
    expect(within(list).queryAllByRole('option')).toHaveLength(0);
    expect(within(list).getByText('Select an endpoint')).toBeInTheDocument();
    expect(screen.getByTestId('ve-effort-select')).toBeDisabled();
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('asks for the name of the endpoint when the block has none', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { model: 'sonnet' },
      })
    );
    expect(endpointSelect()).toHaveTextContent('Select an endpoint');
    expect(screen.getByTestId('ve-model-warning')).toHaveAttribute(
      'title',
      'agent.run needs the name of an endpoint'
    );
  });

  // A text endpoint is a value of the wrong type, and `model` beside the
  // block is an unknown key: the node edits neither.
  it('draws the round node for a text endpoint, and ignores a model beside the block', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    const { unmount } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: 'cle1',
        model: 'claude-haiku-5',
      })
    );
    expect(screen.queryByRole('combobox')).toBeNull();
    unmount();
    renderNode(
      modelNode('agent.run', { provider: 'claude', model: 'claude-haiku-5' })
    );
    expect(endpointSelect()).toHaveTextContent('Select an endpoint');
    expect(modelSelect()).toHaveTextContent('(choose a model)');
  });

  // An endpoint with no models list offers no model to pick: the aliases of
  // the CLI alone would hide the model of the YAML.
  it('has a text field that shows the model of an endpoint with no list', () => {
    mocks.choices = served(choice('cle1', 'anthropic'));
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'my-fine-tune' },
      })
    );
    expect(screen.queryByTestId('ve-model-select')).toBeNull();
    expect(modelField()).toHaveValue('my-fine-tune');
    fireEvent.change(modelField(), { target: { value: 'other' } });
    fireEvent.blur(modelField());
    expect(edits).toEqual([setField(['with', 'endpoint', 'model'], 'other')]);
  });

  it('removes a model that the new endpoint does not list in the same edit as the endpoint', () => {
    mocks.choices = served(
      { ...described(), models: MODELS, name: 'cle1' },
      choice('cle2', 'anthropic', ['other-model'])
    );
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'claude-sonnet-5-5', effort: 'high' },
      })
    );
    pickOption(endpointSelect(), 'cle2');
    // The model goes with the endpoint, and the effort goes with the model:
    // the levels belong to it. The picker then asks for a model again.
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'name'], 'cle2'),
      setField(['with', 'endpoint', 'model'], undefined),
      setField(['with', 'endpoint', 'effort'], undefined),
    ]);
  });

  it('keeps the model when the new endpoint lists it, and when it has no list', () => {
    mocks.choices = served(
      choice('cle1', 'anthropic', MODELS),
      choice('cle2', 'anthropic', ['claude-haiku-5', 'claude-sonnet-5-5']),
      choice('cle3', 'anthropic')
    );
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'claude-haiku-5' },
      })
    );
    pickOption(endpointSelect(), 'cle2');
    pickOption(endpointSelect(), 'cle3');
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'name'], 'cle2'),
      setField(['with', 'endpoint', 'name'], 'cle3'),
    ]);
  });

  it('asks for a model again when the YAML names one that the endpoint lacks, and leaves the YAML alone', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'my-fine-tune' },
      })
    );
    expect(modelSelect()).toHaveTextContent('(choose a model)');
    expect(screen.queryByText('my-fine-tune')).toBeNull();
    // The marker still names the problem, and showing it edits nothing.
    expect(screen.getByTestId('ve-model-warning')).toBeInTheDocument();
    expect(edits).toEqual([]);
  });

  it('removes the model of a chat step in block form when the new endpoint does not list it, as for an agent step', () => {
    mocks.choices = served(
      choice('cle1', 'anthropic', MODELS),
      choice('gpt', 'openai', ['gpt-5'])
    );
    const { edits } = renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'gpt', model: 'gpt-5' },
      })
    );
    pickOption(endpointSelect(), 'cle1');
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'name'], 'cle1'),
      setField(['with', 'endpoint', 'model'], undefined),
    ]);
  });

  it('marks a chat step with no endpoint and no provider in red, as an agent step', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    renderNode(modelNode('chat.completion', { model: 'gpt-5' }));
    const mark = screen.getByTestId('ve-model-warning');
    expect(mark).toHaveAttribute(
      'title',
      'chat.completion needs an endpoint or a provider; add an endpoint on the AI page and name it here'
    );
    expect(mark).toHaveClass('text-destructive');
  });

  it('keeps the model of a chat step editable with the provider of the old method', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    renderNode(
      modelNode('chat.completion', { provider: 'openai', model: 'gpt-5' })
    );
    // The old method names a provider: the picker shows it, never "(none)" over a model.
    expect(endpointSelect()).toHaveTextContent('openai (provider)');
    const warning =
      'The provider "openai" is deprecated: the step calls a model API with its own address and key. Pick an endpoint: the server keeps its key.';
    expect(endpointSelect()).toHaveAttribute('title', warning);
    // A visible amber warning says that the provider is deprecated.
    expect(screen.getByTestId('ve-model-warning')).toHaveAttribute(
      'title',
      warning
    );
    expect(screen.getByTestId('ve-model-warning')).toHaveClass('text-warning');
    // The provider is a value that the picker shows, never an item to pick.
    expect(optionNames(endpointSelect())).toEqual(['gpt']);
    expect(modelField()).toBeEnabled();
  });

  it('keeps a reference as one more option, offers no way to clear it, and follows the YAML', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    const { edits, rerender } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: '${params.MODEL}' },
      })
    );
    expect(modelSelect()).toHaveTextContent('${params.MODEL}');
    expect(optionNames(modelSelect())[0]).toBe('${params.MODEL}');
    expect(optionNames(modelSelect())).not.toContain('(choose a model)');
    expect(edits).toEqual([]);

    // An undo changes the YAML under the select: the select follows it.
    rerender(
      show(
        modelNode('agent.run', {
          provider: 'claude',
          endpoint: { name: 'cle1', model: 'claude-opus-5-5' },
        })
      ).ui
    );
    expect(modelSelect()).toHaveTextContent('claude-opus-5-5');
  });

  it('shows an amber marker with a one-line tooltip for a model that the list lacks, and for no other text', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    const { rerender } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'my-fine-tune' },
      })
    );
    const mark = screen.getByTestId('ve-model-warning');
    expect(mark).toHaveAttribute(
      'title',
      'The model "my-fine-tune" is not in the list of the endpoint "cle1". The step fails when it starts, unless a refresh of the list finds it.'
    );
    expect(mark.getAttribute('title')).not.toContain('\n');

    // A reference and an alias of the CLI claude: no marker.
    for (const model of [
      '${params.MODEL}',
      'sonnet',
      'opusplan',
      'sonnet[1m]',
      'claude-opus-5-5[1m]',
    ]) {
      rerender(
        show(
          modelNode('agent.run', {
            provider: 'claude',
            endpoint: { name: 'cle1', ...(model ? { model } : {}) },
          })
        ).ui
      );
      expect(screen.queryByTestId('ve-model-warning')).toBeNull();
    }
  });

  it('shows a red marker for an agent step that has an endpoint and no model', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    renderNode(
      modelNode('agent.run', { provider: 'claude', endpoint: { name: 'cle1' } })
    );
    const mark = screen.getByTestId('ve-model-warning');
    expect(mark).toHaveAttribute(
      'title',
      'agent.run needs a model; choose one that the endpoint lists'
    );
    expect(mark).toHaveClass('text-destructive');
    expect(modelSelect()).toHaveClass('border-destructive');
  });

  it('shows an endpoint that the server does not know as a value, with a marker', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'typed-in-yaml', model: 'x' },
      })
    );
    expect(endpointSelect()).toHaveTextContent('typed-in-yaml');
    expect(optionNames(endpointSelect())).toEqual(['cle1', 'typed-in-yaml']);
    // The endpoint is named, so the marker is the amber one of an unknown name.
    const mark = screen.getByTestId('ve-model-warning');
    expect(mark).toHaveAttribute(
      'title',
      'The server does not know the endpoint "typed-in-yaml", or this DAG may not use it. The step fails when it starts.'
    );
    expect(mark).toHaveClass('text-warning');
  });

  it('makes no claim before the choices have arrived, for an endpoint with no list and for an older server', () => {
    mocks.choices = undefined;
    const { rerender } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'x' },
      })
    );
    expect(screen.queryByTestId('ve-model-warning')).toBeNull();

    mocks.choices = served(choice('cle1', 'anthropic', []));
    rerender(
      show(
        modelNode('agent.run', {
          provider: 'claude',
          endpoint: { name: 'cle1', model: 'x' },
        })
      ).ui
    );
    expect(screen.queryByTestId('ve-model-warning')).toBeNull();

    // A server that sends no aliases makes no model check.
    mocks.choices = { choices: [choice('cle1', 'anthropic', MODELS)] };
    rerender(
      show(
        modelNode('agent.run', {
          provider: 'claude',
          endpoint: { name: 'cle1', model: 'x' },
        })
      ).ui
    );
    expect(screen.queryByTestId('ve-model-warning')).toBeNull();
  });

  // The pickers edit the model: the icon only shows the provider.
  it('opens no form from a double click or the keys on the Model card, and its icon is no button', () => {
    mocks.choices = served();
    const onCommand = vi.fn();
    renderNode(modelNode('agent.run', { provider: 'claude' }), { onCommand });
    const icon = screen.getByTestId('ve-canvas-sub-icon');
    fireEvent.doubleClick(icon);
    fireEvent.doubleClick(screen.getByRole('group', { name: 'Model' }));
    fireEvent.keyDown(icon, { key: 'Enter' });
    fireEvent.keyDown(icon, { key: ' ' });
    expect(onCommand).not.toHaveBeenCalled();
    expect(icon).toHaveAttribute('aria-hidden', 'true');
    expect(icon).not.toHaveAttribute('role');
    expect(icon).not.toHaveAttribute('tabindex');
    expect(screen.queryByRole('button', { name: /form/i })).toBeNull();
  });

  // The marker sits inside its picker: the picker keeps its width.
  it('draws the marker of a problem inside the picker that it names', () => {
    mocks.choices = served();
    renderNode(modelNode('agent.run', { provider: 'claude' }));
    const marker = screen.getByTestId('ve-model-warning');
    expect(endpointSelect()).toContainElement(marker);
    expect(marker).toHaveClass('text-destructive');
  });
});

describe('the Model node of a chat step', () => {
  it('offers the endpoints of both protocols, and an endpoint takes the place of the provider', () => {
    mocks.choices = served(
      choice('cle1', 'anthropic', MODELS),
      choice('gpt', 'openai', ['gpt-5'])
    );
    const { edits } = renderNode(
      modelNode('chat.completion', { provider: 'openai', model: 'gpt-5' })
    );
    expect(optionNames(endpointSelect())).toEqual(['cle1', 'gpt']);
    expect(screen.queryByTestId('ve-model-cli')).toBeNull();
    pickOption(endpointSelect(), 'gpt');
    // The first endpoint converts the old method to the block in one edit.
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'name'], 'gpt'),
      setField(['with', 'endpoint', 'model'], 'gpt-5'),
      setField(['with', 'provider'], undefined),
      setField(['with', 'model'], undefined),
    ]);
  });

  it('converts every key of the old method in one edit, and drops a model that the endpoint lacks', () => {
    mocks.choices = served(
      { ...described(), models: MODELS, name: 'cle2' },
      choice('gpt', 'openai', ['gpt-5'])
    );
    const old = {
      provider: 'local',
      base_url: 'http://localhost:8080',
      api_key_name: 'KEY',
      model: SONNET,
      thinking: { enabled: true, effort: 'high', budget_tokens: 1024 },
    };
    const { edits } = renderNode(modelNode('chat.completion', old));
    // The old method has no Effort picker.
    expect(screen.queryByRole('combobox', { name: 'Effort' })).toBeNull();
    pickOption(endpointSelect(), 'cle2');
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'name'], 'cle2'),
      setField(['with', 'endpoint', 'model'], SONNET),
      setField(['with', 'endpoint', 'effort'], 'high'),
      setField(['with', 'provider'], undefined),
      setField(['with', 'base_url'], undefined),
      setField(['with', 'api_key_name'], undefined),
      setField(['with', 'model'], undefined),
      setField(['with', 'thinking'], undefined),
    ]);
    edits.length = 0;
    pickOption(endpointSelect(), 'gpt');
    // The effort goes with the model, as in the block form: a model comes next.
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'name'], 'gpt'),
      setField(['with', 'provider'], undefined),
      setField(['with', 'base_url'], undefined),
      setField(['with', 'api_key_name'], undefined),
      setField(['with', 'model'], undefined),
      setField(['with', 'thinking'], undefined),
    ]);
  });

  it('writes only the block for an empty chat step, and draws the Effort picker', () => {
    mocks.choices = served(described());
    const { edits } = renderNode(modelNode('chat.completion', {}));
    expect(effortSelect()).toBeDisabled();
    pickOption(endpointSelect(), 'cle1');
    expect(edits).toEqual([setField(['with', 'endpoint', 'name'], 'cle1')]);
  });

  it('marks a chat step in block form as an agent step: red for a missing name or model, amber for a model that the list lacks', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    const { rerender } = renderNode(
      modelNode('chat.completion', { endpoint: { model: 'gpt-5' } })
    );
    expect(endpointSelect()).toHaveTextContent('Select an endpoint');
    const noName = screen.getByTestId('ve-model-warning');
    expect(noName).toHaveAttribute(
      'title',
      'chat.completion needs the name of an endpoint'
    );
    expect(noName).toHaveClass('text-destructive');
    expect(endpointSelect()).toHaveClass('border-destructive');

    rerender(
      show(modelNode('chat.completion', { endpoint: { name: 'gpt' } })).ui
    );
    const noModel = screen.getByTestId('ve-model-warning');
    expect(noModel).toHaveAttribute(
      'title',
      'chat.completion needs a model; choose one that the endpoint lists'
    );
    expect(noModel).toHaveClass('text-destructive');
    expect(modelSelect()).toHaveClass('border-destructive');

    rerender(
      show(
        modelNode('chat.completion', {
          endpoint: { name: 'gpt', model: 'gpt-5-ghost' },
        })
      ).ui
    );
    const ghost = screen.getByTestId('ve-model-warning');
    expect(ghost).toHaveClass('text-warning');
    expect(modelSelect()).toHaveClass('border-warning');
  });

  it('keeps the model and the effort that the new endpoint supports, in block form', () => {
    mocks.choices = served(
      { ...described('openai'), name: 'cle1' },
      { ...described('openai'), name: 'cle2' }
    );
    const { edits } = renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'cle1', model: SONNET, effort: 'high' },
      })
    );
    pickOption(endpointSelect(), 'cle2');
    expect(edits).toEqual([setField(['with', 'endpoint', 'name'], 'cle2')]);
  });

  it('has no (none) item for a chat step in block form, as for an agent step', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'gpt', model: 'gpt-5' },
      })
    );
    expect(optionNames(endpointSelect())).toEqual(['gpt']);
  });

  it('writes the name of the model with the edit of the form, and marks a name that the list lacks', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5', 'gpt-5-mini']));
    const { edits } = renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'gpt', model: 'gpt-5-ghost' },
      })
    );
    expect(screen.getByTestId('ve-model-warning')).toBeInTheDocument();
    pickOption(modelSelect(), 'gpt-5-mini');
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'model'], 'gpt-5-mini'),
    ]);
  });

  it('has a text field for a model when no list is there to choose from', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    const { edits } = renderNode(
      modelNode('chat.completion', { provider: 'openai', model: 'gpt-5' })
    );
    expect(screen.queryByTestId('ve-model-select')).toBeNull();
    fireEvent.change(modelField(), { target: { value: 'gpt-5-mini' } });
    fireEvent.blur(modelField());
    expect(edits).toEqual([setField(['with', 'model'], 'gpt-5-mini')]);
  });

  it('does not accept an alias of the CLI: a chat step sends the name to the API', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'gpt', model: 'sonnet' },
      })
    );
    expect(screen.getByTestId('ve-model-warning')).toBeInTheDocument();
  });

  it('has one block of controls for each model of a list, and an entry needs an endpoint or a provider', () => {
    mocks.choices = served(
      choice('cle1', 'anthropic', MODELS),
      choice('gpt', 'openai', ['gpt-5', 'gpt-5-mini'])
    );
    const { edits } = renderNode(
      modelNode('chat.completion', {
        model: [
          { endpoint: 'gpt', name: 'gpt-5' },
          { endpoint: 'cle1', name: 'claude-haiku-5' },
          { name: 'gpt-5-ghost' },
        ],
      })
    );
    const rows = screen.getAllByTestId('ve-model-row');
    expect(rows).toHaveLength(3);
    const selects = [1, 2, 3].map((index) =>
      screen.getByLabelText(`Endpoint of model ${index}`)
    );
    expect(selects.map((select) => select.textContent)).toEqual([
      'gpt',
      'cle1',
      'Select an endpoint',
    ]);
    // No Effort picker: a list has no block.
    expect(screen.queryByRole('combobox', { name: 'Effort' })).toBeNull();
    // Only the entry with no endpoint and no provider has a marker.
    expect(within(rows[0]!).queryByTestId('ve-model-warning')).toBeNull();
    expect(within(rows[1]!).queryByTestId('ve-model-warning')).toBeNull();
    expect(
      within(rows[2]!).getByTestId('ve-model-warning')
    ).toBeInTheDocument();

    // The name of a map row, as the form writes it.
    pickOption(screen.getByLabelText('Name of model 1'), 'gpt-5-mini');
    expect(edits).toEqual([
      setField(['with', 'model', 0, 'name'], 'gpt-5-mini'),
    ]);

    // The endpoint of an entry.
    edits.length = 0;
    pickOption(selects[2]!, 'cle1');
    pickOption(selects[0]!, 'cle1');
    expect(edits).toEqual([
      setField(['with', 'model', 2, 'endpoint'], 'cle1'),
      setField(['with', 'model', 0, 'endpoint'], 'cle1'),
    ]);
  });

  it('draws the round node for a list that holds a bare name, which the server refuses', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    renderNode(
      modelNode('chat.completion', {
        model: [{ endpoint: 'gpt', name: 'gpt-5' }, 'gpt-5-mini'],
      })
    );
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('takes the provider of the old method out of a row that gets an endpoint', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    const { edits } = renderNode(
      modelNode('chat.completion', {
        model: [{ provider: 'openai', name: 'gpt-5' }],
      })
    );
    pickOption(screen.getAllByRole('combobox')[0]!, 'gpt');
    expect(edits).toEqual([
      setField(['with', 'model', 0, 'endpoint'], 'gpt'),
      setField(['with', 'model', 0, 'provider'], undefined),
    ]);
  });
});

describe('the Model node in a compact canvas and in a run view', () => {
  it('shows text only in a compact canvas: no control, and the marker stays', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'my-fine-tune' },
      }),
      { compact: true }
    );
    expect(
      screen.getByTestId('ve-canvas-node-toolsat:steps[0]:model')
    ).toHaveAttribute('data-shape', 'model');
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(
      screen.getByRole('button', { name: 'my-fine-tune' })
    ).toBeInTheDocument();
    expect(screen.getByTestId('ve-model-warning')).toBeInTheDocument();
  });

  it('is read-only in a run view: no control, no marker, and no request', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    renderNode(
      modelNode(
        'agent.run',
        {
          provider: 'claude',
          endpoint: { name: 'cle1', model: 'my-fine-tune' },
        },
        false
      ),
      { editable: false, readOnly: true }
    );
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.queryByTestId('ve-model-warning')).toBeNull();
    expect(
      screen.getByRole('button', { name: 'my-fine-tune' })
    ).toBeInTheDocument();
    expect(mocks.queries.every((init) => init === null)).toBe(true);
  });

  it('keeps the card with disabled controls while the canvas is frozen', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'claude-haiku-5' },
      }),
      { editable: false }
    );
    expect(endpointSelect()).toBeDisabled();
    expect(modelSelect()).toBeDisabled();
    expect(edits).toEqual([]);
  });

  it('draws the round node for a model that only the YAML edits', () => {
    mocks.choices = served();
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: { name: 'x' } },
      })
    );
    expect(screen.queryByRole('combobox')).toBeNull();
  });
});

// The Model node picks the endpoint and the model in a styled list, and the
// effort of the model in a row under the list.

const INFOS = [
  {
    id: 'claude-opus-5-5',
    displayName: 'Claude Opus 5.5',
    efforts: ['low', 'medium', 'high', 'xhigh', 'max'],
  },
  {
    id: 'claude-sonnet-5-5',
    displayName: 'Claude Sonnet 5.5',
    efforts: ['low', 'medium', 'high'],
  },
  { id: 'claude-haiku-5', displayName: 'Claude Haiku 5', efforts: [] },
];

const SONNET = 'claude-sonnet-5-5';

/** The choice of an endpoint whose server sends the infos of its models. */
const described = (protocol: 'anthropic' | 'openai' = 'anthropic') => ({
  ...choice(
    'cle1',
    protocol,
    INFOS.map((info) => info.id)
  ),
  modelInfo: INFOS,
});

const effortSelect = () => screen.getByRole('combobox', { name: 'Effort' });
const effortLevels = () => {
  open(effortSelect());
  const levels = names();
  fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' });
  return levels;
};
const open = (trigger: HTMLElement) => fireEvent.click(trigger);
const names = () =>
  within(screen.getByRole('listbox'))
    .getAllByRole('option')
    .map((option) => option.textContent);

describe('the pickers of the Model node', () => {
  it('shows the display name of the model in the trigger and its id under the name in the list', () => {
    mocks.choices = served(described());
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: SONNET, effort: 'high' },
      })
    );
    expect(modelSelect()).toHaveTextContent('Claude Sonnet 5.5');
    expect(modelSelect()).not.toHaveTextContent(SONNET);
    open(modelSelect());
    const options = within(screen.getByRole('listbox')).getAllByRole('option');
    expect(options[0]).toHaveTextContent('Claude Opus 5.5claude-opus-5-5');
    expect(options[1]).toHaveTextContent(`Claude Sonnet 5.5${SONNET}`);
    // The current item has the check mark and the state selected.
    expect(options[1]).toHaveAttribute('aria-selected', 'true');
    expect(options[0]).toHaveAttribute('aria-selected', 'false');
    // The aliases of the CLI have no display name: the name is the id.
    expect(names().slice(-2)).toEqual(['opusplan', 'default']);
  });

  it('opens with the arrow keys, moves with them, picks with Enter and shuts with Escape', async () => {
    mocks.choices = served(
      choice('cle1', 'anthropic', MODELS),
      choice('cle2', 'anthropic'),
      choice('cle3', 'anthropic')
    );
    const user = userEvent.setup();
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { model: 'sonnet' },
      })
    );
    endpointSelect().focus();
    await user.keyboard('{ArrowDown}');
    expect(endpointSelect()).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('listbox')).toHaveFocus();
    // The first item, cle1, is the active one: an agent step has no (none).
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowUp}');
    await user.keyboard('{Enter}');
    expect(edits).toEqual([setField(['with', 'endpoint', 'name'], 'cle2')]);
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(endpointSelect()).toHaveFocus();

    await user.keyboard('{ArrowDown}{End}{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(endpointSelect()).toHaveFocus();
    expect(edits).toHaveLength(1);
  });

  it('jumps to an item by the first letters typed in the list', async () => {
    mocks.choices = served(
      choice('alpha', 'anthropic'),
      choice('beta', 'anthropic'),
      choice('berry', 'anthropic')
    );
    const user = userEvent.setup();
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { model: 'sonnet' },
      })
    );
    endpointSelect().focus();
    await user.keyboard('{ArrowDown}ber{Enter}');
    expect(edits).toEqual([setField(['with', 'endpoint', 'name'], 'berry')]);
  });

  it('has a search box when the list is long, with the focus in it', async () => {
    const many = Array.from({ length: 10 }, (_, i) => `gpt-${i}`);
    mocks.choices = served(choice('gpt', 'openai', many));
    const user = userEvent.setup();
    const { edits } = renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'gpt', model: 'gpt-0' },
      })
    );
    open(modelSelect());
    const search = screen.getByRole('textbox', { name: 'Search the models' });
    expect(search).toHaveFocus();
    await user.keyboard('gpt-7');
    expect(names()).toEqual(['gpt-7']);
    await user.keyboard('{Enter}');
    expect(edits).toEqual([setField(['with', 'endpoint', 'model'], 'gpt-7')]);

    // A short list has no search box.
    open(endpointSelect());
    expect(
      screen.queryByRole('textbox', { name: 'Search the models' })
    ).toBeNull();
  });

  it('shuts with a click outside, and a double click in the list never reaches the node', () => {
    mocks.choices = served(described());
    const onCommand = vi.fn();
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1' },
      }),
      { onCommand }
    );
    open(modelSelect());
    fireEvent.doubleClick(screen.getByRole('listbox'));
    expect(onCommand).not.toHaveBeenCalled();
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('draws the list in a portal outside the node, and the controls carry nodrag nopan', () => {
    mocks.choices = served(described());
    renderNode(
      modelNode('agent.run', { provider: 'claude', endpoint: { name: 'cle1' } })
    );
    expect(modelSelect()).toHaveClass('nodrag', 'nopan');
    expect(endpointSelect()).toHaveClass('nodrag', 'nopan');
    open(modelSelect());
    const card = screen.getByTestId('ve-canvas-node-toolsat:steps[0]:model');
    expect(card.contains(screen.getByRole('listbox'))).toBe(false);
    expect(screen.getByTestId('ai-picker-popover')).toHaveClass('nodrag');
  });

  it('does not open while the canvas is frozen', () => {
    mocks.choices = served(described());
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1' },
      }),
      { editable: false }
    );
    open(modelSelect());
    open(endpointSelect());
    expect(screen.queryByRole('listbox')).toBeNull();
  });
});

describe('the Effort picker under the Model picker', () => {
  it('offers Default and only the levels that the model supports, and writes the pick in with.effort of an agent step', () => {
    mocks.choices = served(described());
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: SONNET },
      })
    );
    expect(effortSelect()).toHaveTextContent('Effort · default');
    expect(effortLevels()).toEqual(['Default', 'low', 'medium', 'high']);
    pickOption(effortSelect(), 'medium');
    expect(edits).toEqual([setField(['with', 'endpoint', 'effort'], 'medium')]);
    // Selecting shuts the list.
    expect(screen.queryByRole('listbox')).toBeNull();
    // The picker is under the Model picker, in the same block.
    const row = screen.getByTestId('ve-model-row');
    const order = [...row.querySelectorAll('[role="combobox"]')].map((item) =>
      item.getAttribute('aria-label')
    );
    expect(order).toEqual(['Endpoint', 'Model', 'Effort']);
  });

  it('shows the effort that the step has, and removes the key with Default', () => {
    mocks.choices = served(described());
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: SONNET, effort: 'high' },
      })
    );
    expect(effortSelect()).toHaveTextContent('Effort · high');
    open(effortSelect());
    expect(
      within(screen.getByRole('listbox')).getByRole('option', { name: 'high' })
    ).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' });
    pickOption(effortSelect(), 'Default');
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'effort'], undefined),
    ]);
  });

  it('offers the levels of the CLI for an agent step when the model has no info, and for an alias', () => {
    mocks.choices = served(choice('cle1', 'anthropic', MODELS));
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'sonnet' },
      })
    );
    expect(effortLevels()).toEqual([
      'Default',
      'low',
      'medium',
      'high',
      'xhigh',
      'max',
    ]);
  });

  it('is drawn disabled with a dash when the model supports no level, so that the node keeps its height', () => {
    mocks.choices = served(described());
    const { rerender } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'claude-haiku-5' },
      })
    );
    expect(effortSelect()).toBeDisabled();
    expect(effortSelect()).toHaveTextContent('Effort · —');

    // A chat step whose model has no info offers no level either.
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    rerender(
      show(
        modelNode('chat.completion', {
          endpoint: { name: 'gpt', model: 'gpt-5' },
        })
      ).ui
    );
    expect(effortSelect()).toBeDisabled();
    expect(effortSelect()).toHaveTextContent('Effort · —');
  });

  // The picker never hides a value of the YAML: "Default" can remove it.
  it('shows an effort that the model offers no level for, and removes it with Default', () => {
    mocks.choices = served(choice('gpt', 'openai', ['gpt-5']));
    const { edits } = renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'gpt', model: 'gpt-5', effort: 'high' },
      })
    );
    expect(effortSelect()).toBeEnabled();
    expect(effortSelect()).toHaveTextContent('Effort · high');
    expect(effortLevels()).toEqual(['Default', 'high']);
    pickOption(effortSelect(), 'Default');
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'effort'], undefined),
    ]);
  });

  it('is disabled while the canvas is frozen', () => {
    mocks.choices = served(described());
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: SONNET },
      }),
      { editable: false }
    );
    open(effortSelect());
    expect(effortSelect()).toBeDisabled();
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('keeps the effort that the new model supports, and clears the one that it does not in the same edit', () => {
    mocks.choices = served(described());
    const { edits } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'claude-opus-5-5', effort: 'xhigh' },
      })
    );
    // Sonnet has no xhigh: the key goes with the change of the model.
    pickOption(modelSelect(), `Claude Sonnet 5.5${SONNET}`);
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'model'], SONNET),
      setField(['with', 'endpoint', 'effort'], undefined),
    ]);

    // An alias of the CLI takes every level: the effort stays.
    edits.length = 0;
    pickOption(modelSelect(), 'opus');
    expect(edits).toEqual([setField(['with', 'endpoint', 'model'], 'opus')]);
  });

  it('writes the effort of a chat step in with.endpoint.effort, for the levels of its model only', () => {
    mocks.choices = served(described('openai'));
    const { edits } = renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'cle1', model: SONNET, effort: 'high' },
      })
    );
    expect(effortSelect()).toHaveTextContent('Effort · high');
    expect(effortLevels()).toEqual(['Default', 'low', 'medium', 'high']);
    pickOption(effortSelect(), 'low');
    expect(edits).toEqual([setField(['with', 'endpoint', 'effort'], 'low')]);

    edits.length = 0;
    pickOption(modelSelect(), 'Claude Haiku 5claude-haiku-5');
    expect(edits).toEqual([
      setField(['with', 'endpoint', 'model'], 'claude-haiku-5'),
      setField(['with', 'endpoint', 'effort'], undefined),
    ]);
  });

  it('has no Effort picker for a chat step with a list of models', () => {
    mocks.choices = served(described());
    renderNode(
      modelNode('chat.completion', {
        model: [
          { endpoint: 'cle1', name: SONNET },
          { endpoint: 'cle1', name: 'claude-opus-5-5' },
        ],
      })
    );
    expect(screen.queryByRole('combobox', { name: 'Effort' })).toBeNull();
  });
});

// The list of a picker follows the scale of the graph and has the width of its
// trigger. jsdom has no layout: the test gives the trigger a layout width and a
// box on the screen.
describe('the list of a picker in a zoomed graph', () => {
  let layoutWidth: ReturnType<typeof vi.spyOn>;
  let box: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    layoutWidth = vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get');
    box = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect');
  });
  afterEach(() => {
    layoutWidth.mockRestore();
    box.mockRestore();
  });

  const rectOf = (
    left: number,
    top: number,
    width: number,
    height: number
  ) => ({
    x: left,
    y: top,
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
    toJSON: () => ({}),
  });

  /** A trigger of 160 layout px, drawn at this scale. */
  function zoom(scale: number, top = 100) {
    layoutWidth.mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('role') === 'combobox' ? 160 : 0;
    });
    box.mockImplementation(function (this: HTMLElement) {
      return this.getAttribute('role') === 'combobox'
        ? rectOf(100, top, 160 * scale, 28 * scale)
        : rectOf(0, 0, 0, 0);
    });
  }

  it('has the layout width of the trigger and the scale of the graph', () => {
    zoom(0.5);
    mocks.choices = served(described());
    renderNode(
      modelNode('agent.run', { provider: 'claude', endpoint: { name: 'cle1' } })
    );
    open(modelSelect());
    const list = screen.getByTestId('ai-picker-popover');
    expect(list.style.width).toBe('160px');
    expect(list.style.minWidth).toBe('160px');
    expect(list.style.transform).toBe('scale(0.5)');
    expect(list.style.transformOrigin).toBe('top left');
    expect(list.style.left).toBe('100px');
    // Flush under the trigger: it covers the border that they share, scaled too.
    expect(list.style.top).toBe(`${100 + 14 - 0.5}px`);
  });

  it('joins its trigger, which shows the open state with a turned chevron', () => {
    zoom(1);
    mocks.choices = served(described());
    renderNode(
      modelNode('agent.run', { provider: 'claude', endpoint: { name: 'cle1' } })
    );
    const trigger = modelSelect();
    expect(trigger).not.toHaveAttribute('data-side');
    open(trigger);
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(trigger).toHaveAttribute('data-side', 'down');
    expect(screen.getByTestId('ai-picker-popover')).toHaveAttribute(
      'data-side',
      'down'
    );
    // The value and the chevron stay in the trigger while the list is open.
    expect(trigger).toHaveTextContent('(choose a model)');
    expect([...trigger.querySelectorAll('svg')].pop()).toHaveClass(
      'group-aria-expanded:rotate-180'
    );
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' });
    expect(trigger).not.toHaveAttribute('data-side');
  });

  it('is not scaled at the scale of the form, and has the width of the trigger', () => {
    zoom(1);
    mocks.choices = served(described());
    renderNode(
      modelNode('agent.run', { provider: 'claude', endpoint: { name: 'cle1' } })
    );
    open(modelSelect());
    const list = screen.getByTestId('ai-picker-popover');
    expect(list.style.transform).toBe('');
    expect(list.style.width).toBe('160px');
  });

  it('opens over a trigger that has no room under it, from its bottom left corner', () => {
    zoom(0.5, window.innerHeight - 20);
    mocks.choices = served(described());
    renderNode(
      modelNode('agent.run', { provider: 'claude', endpoint: { name: 'cle1' } })
    );
    open(modelSelect());
    const list = screen.getByTestId('ai-picker-popover');
    expect(list.style.transformOrigin).toBe('bottom left');
    expect(list.style.bottom).not.toBe('');
    expect(list.style.top).toBe('');
    // The trigger and the list join on the side of the trigger that faces it.
    expect(list).toHaveAttribute('data-side', 'up');
    expect(modelSelect()).toHaveAttribute('data-side', 'up');
  });
});

// An agent step and a chat step on one endpoint show the logo of its preset.
describe('the icon of the Model node', () => {
  const iconClass = () =>
    screen
      .getByTestId('ve-canvas-sub-icon')
      .querySelector('svg')
      ?.getAttribute('class') ?? '';

  it('is the logo of the preset of the endpoint for an agent step and a chat step', () => {
    mocks.choices = {
      choices: [
        { ...choice('main', 'anthropic', MODELS), preset: 'anthropic' },
      ],
    };
    const { unmount } = renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'main', model: 'claude-haiku-5' },
      })
    );
    const agent = iconClass();
    unmount();
    renderNode(
      modelNode('chat.completion', {
        endpoint: { name: 'main', model: 'claude-haiku-5' },
      })
    );
    expect(agent).toContain('anthropic');
    expect(iconClass()).toBe(agent);
  });

  it('is the bot for an endpoint whose preset has no logo', () => {
    mocks.choices = served(choice('plain', 'anthropic', MODELS));
    renderNode(
      modelNode('agent.run', {
        provider: 'claude',
        endpoint: { name: 'plain', model: 'claude-haiku-5' },
      })
    );
    expect(iconClass()).toContain('bot');
  });
});
