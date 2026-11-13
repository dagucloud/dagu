// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  optionNames,
  pickOption,
} from '@/features/ai-endpoints/__tests__/pickerHelpers';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { DagEditOp, StepView, ValidationIssue } from '../../contracts';
import { makeStep } from '../../canvas/__tests__/fixtures';
import { ModelSection } from '../sections/chat/ModelSection';

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    if (path === '/ai/endpoint-choices') {
      mocks.choicesInit = init;
      return { data: init === null ? undefined : mocks.choices };
    }
    return { data: undefined };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const mocks = vi.hoisted(() => ({
  /** The answer of the route of endpoint choices; undefined while it loads. */
  choices: undefined as unknown,
  /** The last query that the form sent for the choices. */
  choicesInit: undefined as unknown,
}));

afterEach(() => {
  vi.clearAllMocks();
  mocks.choices = undefined;
  mocks.choicesInit = undefined;
});

const choice = (name: string, protocol = 'anthropic') => ({
  name,
  protocol,
  preset: protocol === 'anthropic' ? 'anthropic' : 'mistral',
  scope: 'global',
});
const dag = { name: 'classify', workspace: 'team' };

const field = (path: (string | number)[], value: unknown): DagEditOp => ({
  kind: 'setStepField',
  step: 'steps[0]',
  path,
  value,
});

const error = (message: string): ValidationIssue => ({
  severity: ValidationIssueSeverity.error,
  source: ValidationIssueSource.build,
  message,
});

function chat(withValue: Record<string, unknown>): StepView {
  return makeStep({
    key: 'steps[0]',
    kind: 'action',
    action: 'chat.completion',
    value: { action: 'chat.completion', with: withValue },
  });
}

function setup(
  step: StepView,
  options: {
    readOnly?: boolean;
    fieldIssues?: Record<string, ValidationIssue[]>;
  } = {}
) {
  const onEdit = vi.fn<(op: DagEditOp) => void>();
  const view = render(
    <ModelSection
      step={step}
      readOnly={options.readOnly ?? false}
      onEdit={onEdit}
      fieldIssues={options.fieldIssues ?? {}}
      candidates={[]}
      dag={dag}
    />
  );
  return { onEdit, ...view };
}

// Spec 093, "The step forms": the Model section of a chat step has an Endpoint
// select, the Model field and the Effort field, as the form of an agent step.
describe('Endpoint select of the Model section of a chat step', () => {
  it('lists the endpoints of every protocol that the DAG may use, and converts the old method on the choice', () => {
    mocks.choices = {
      choices: [choice('cle1'), choice('mistral1', 'openai')],
    };
    const { onEdit } = setup(chat({ model: 'm', prompt: 'hi' }));

    // The form asks for the endpoints of this DAG in its workspace.
    expect(mocks.choicesInit).toEqual({
      params: {
        query: { remoteNode: 'local', dag: 'classify', workspace: 'team' },
      },
    });
    const select = screen.getByLabelText('Endpoint');
    expect(optionNames(select)).toEqual(['(none)', 'cle1', 'mistral1']);
    pickOption(select, 'mistral1');
    // One edit: the endpoint, the model that moves, and the key that goes.
    expect(onEdit.mock.calls.map(([op]) => op)).toEqual([
      field(['with', 'endpoint', 'name'], 'mistral1'),
      field(['with', 'endpoint', 'model'], 'm'),
      field(['with', 'model'], undefined),
    ]);
  });

  it('writes the fields of the block for a step with no model information', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const { onEdit } = setup(chat({ prompt: 'hi' }));
    pickOption(screen.getByLabelText('Endpoint'), 'cle1');
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'endpoint', 'name'], 'cle1')
    );
    const model = screen.getByLabelText('Model');
    fireEvent.change(model, { target: { value: 'claude-haiku-4-5' } });
    fireEvent.blur(model);
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'endpoint', 'model'], 'claude-haiku-4-5')
    );
    // The Effort field waits for an endpoint.
    expect(screen.getByLabelText('Effort')).toBeDisabled();
  });

  it('shows Name, Model and Effort of the block, in this order, and writes each one', () => {
    mocks.choices = {
      choices: [
        {
          ...choice('cle1'),
          models: ['m', 'm2'],
          modelInfo: [
            { id: 'm', efforts: ['low', 'high'] },
            { id: 'm2', efforts: [] },
          ],
        },
      ],
    };
    const { onEdit, container } = setup(
      chat({ endpoint: { name: 'cle1', model: 'm', effort: 'high' } })
    );
    const boxes = [
      ...container.querySelectorAll<HTMLElement>('[data-field-path]'),
    ].map((box) => box.dataset.fieldPath);
    expect(boxes.filter((path) => path?.startsWith('with.endpoint'))).toEqual([
      'with.endpoint.name',
      'with.endpoint.model',
      'with.endpoint.effort',
    ]);
    const endpoint = screen.getByLabelText('Endpoint');
    expect(endpoint).toHaveTextContent('cle1');
    // A step in block form needs an endpoint: there is no (none) item.
    expect(optionNames(endpoint)).toEqual(['cle1']);
    const effort = screen.getByLabelText('Effort');
    expect(effort).toHaveTextContent('high');
    expect(optionNames(effort)).toEqual(['Default', 'low', 'high']);
    pickOption(effort, 'low');
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'endpoint', 'effort'], 'low')
    );
    pickOption(effort, 'Default');
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'endpoint', 'effort'], undefined)
    );
    const model = screen.getByLabelText('Model');
    fireEvent.change(model, { target: { value: 'm2' } });
    fireEvent.blur(model);
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'endpoint', 'model'], 'm2')
    );
  });

  it('asks for the name and for the model of the block, as an agent step does', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const none = setup(chat({ endpoint: { model: 'm' } }));
    expect(
      screen.getByText('chat.completion needs the name of an endpoint')
    ).toBeInTheDocument();
    none.unmount();
    setup(chat({ endpoint: { name: 'cle1' } }));
    expect(
      screen.getByText(
        'chat.completion needs a model; choose one that the endpoint lists'
      )
    ).toBeInTheDocument();
  });

  it('shows an issue of the server under the field that its path names', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(chat({ endpoint: { name: 'cle1', model: 'm', effort: 'warp' } }), {
      fieldIssues: {
        'with.endpoint.name': [error('name issue')],
        'with.endpoint.model': [error('model issue')],
        'with.endpoint.effort': [error('effort issue')],
      },
    });
    for (const message of ['name issue', 'model issue', 'effort issue']) {
      expect(screen.getByText(message)).toBeInTheDocument();
    }
  });

  it('hides the fields of the old method in block form, and the Effort in the old method', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const old = setup(chat({ provider: 'openai', model: 'm' }));
    expect(screen.getByLabelText('Provider')).toBeInTheDocument();
    expect(screen.getByTestId('ve-model-advanced')).toBeInTheDocument();
    expect(screen.queryByLabelText('Effort')).toBeNull();
    old.unmount();
    setup(chat({ endpoint: { name: 'cle1', model: 'm' } }));
    expect(screen.queryByLabelText('Provider')).toBeNull();
    expect(screen.queryByTestId('ve-model-advanced')).toBeNull();
    expect(screen.getByLabelText('Model')).toHaveValue('m');
  });

  // The Provider stays visible with the block, so that the contradiction can go.
  it('shows the contradiction of the server, and keeps the Provider to remove', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const message =
      'key "endpoint" and key "provider" contradict each other; an endpoint gives the provider type';
    const { onEdit } = setup(
      chat({ endpoint: { name: 'cle1', model: 'm' }, provider: 'openai' }),
      { fieldIssues: { 'with.endpoint': [error(message)] } }
    );
    expect(screen.getByText(message)).toBeInTheDocument();
    const provider = screen.getByLabelText('Provider');
    expect(provider).toHaveValue('openai');
    fireEvent.change(provider, { target: { value: '' } });
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'provider'], undefined));
  });

  it('keeps the base URL and the API key variable available, and says that the endpoint holds them', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(
      chat({
        endpoint: { name: 'cle1', model: 'm' },
        base_url: 'https://x.test',
        api_key_name: 'KEY',
      })
    );
    expect(screen.getByLabelText('Base URL')).toHaveValue('https://x.test');
    expect(
      screen.getByText(
        'An endpoint holds its own base URL. Remove this key, or remove the endpoint.'
      )
    ).toBeInTheDocument();
    expect(screen.getByLabelText('API key variable')).toHaveValue('KEY');
    expect(
      screen.getByText(
        'An endpoint holds its own key. Remove this key, or remove the endpoint.'
      )
    ).toBeInTheDocument();
  });

  it('shows the usual help for the base URL and the key without an endpoint', () => {
    setup(chat({ provider: 'local', api_key_name: 'KEY' }));
    expect(
      screen.getByText(
        'The endpoint of the server. It replaces the default one.'
      )
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        'The name of the environment variable that holds the API key.'
      )
    ).toBeInTheDocument();
  });

  it('warns about a name that the server does not know, and keeps it as a value', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(chat({ endpoint: { name: 'typed-in-yaml', model: 'm' } }));
    expect(screen.getByLabelText('Endpoint')).toHaveTextContent(
      'typed-in-yaml'
    );
    expect(
      screen.getByText(/The server does not know the endpoint "typed-in-yaml"/)
    ).toBeInTheDocument();
  });

  it('accepts an endpoint of the protocol openai with no protocol warning', () => {
    mocks.choices = { choices: [choice('mistral1', 'openai')] };
    setup(chat({ endpoint: { name: 'mistral1', model: 'm' } }));
    expect(screen.getByLabelText('Endpoint')).toHaveTextContent('mistral1');
    expect(screen.queryByText(/does not have the protocol/)).toBeNull();
    expect(screen.queryByText(/does not know the endpoint/)).toBeNull();
  });

  it('points to the AI page when no endpoint is available', () => {
    mocks.choices = { choices: [] };
    setup(chat({ model: 'm' }));
    expect(screen.queryByLabelText('Endpoint')).toBeNull();
    expect(screen.getByTestId('ve-endpoint-empty')).toHaveTextContent(
      'No endpoint is available for this DAG.'
    );
    // The old method stays: the Provider is there.
    expect(screen.getByLabelText('Provider')).toBeInTheDocument();
  });

  it('is disabled for a reader', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(chat({ endpoint: { name: 'cle1', model: 'm' } }), {
      readOnly: true,
    });
    expect(screen.getByLabelText('Endpoint')).toBeDisabled();
    expect(screen.getByLabelText('Effort')).toBeDisabled();
  });

  it('shows a value that is not a map as YAML only', () => {
    const text = setup(chat({ endpoint: 'cle1', model: 'm' }));
    expect(screen.queryByLabelText('Endpoint')).toBeNull();
    text.unmount();
    setup(chat({ endpoint: ['cle1'], model: 'm' }));
    expect(screen.queryByLabelText('Endpoint')).toBeNull();
  });

  it('asks for no endpoint of a DAG with no name', () => {
    const onEdit = vi.fn();
    render(
      <ModelSection
        step={chat({ model: 'm' })}
        readOnly={false}
        onEdit={onEdit}
        fieldIssues={{}}
        candidates={[]}
        dag={{ name: ' ' }}
      />
    );
    expect(mocks.choicesInit).toBeNull();
  });
});

// A row of a model list can name its own endpoint or provider.
describe('Endpoint select of a model row', () => {
  const list = (extra: Record<string, unknown> = {}) =>
    chat({
      model: [
        { name: 'claude-sonnet-4-6', endpoint: 'cle1' },
        { name: 'gpt-4o', provider: 'openai' },
      ],
      ...extra,
    });

  it('shows the endpoint of a row and no provider for it, and the provider of the other row', () => {
    mocks.choices = {
      choices: [choice('cle1'), choice('mistral1', 'openai')],
    };
    setup(list());
    expect(screen.getByLabelText('Endpoint of model 1')).toHaveTextContent(
      'cle1'
    );
    expect(screen.queryByLabelText('Provider of model 1')).toBeNull();
    expect(screen.getByLabelText('Endpoint of model 2')).toHaveTextContent(
      '(none)'
    );
    expect(screen.getByLabelText('Provider of model 2')).toHaveValue('openai');
  });

  it('writes the endpoint of a row and removes its provider', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const { onEdit } = setup(list());
    pickOption(screen.getByLabelText('Endpoint of model 2'), 'cle1');
    expect(onEdit).toHaveBeenNthCalledWith(
      1,
      field(['with', 'model', 1, 'endpoint'], 'cle1')
    );
    expect(onEdit).toHaveBeenNthCalledWith(
      2,
      field(['with', 'model', 1, 'provider'], undefined)
    );
  });

  it('removes the endpoint of a row for None', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const { onEdit } = setup(list());
    pickOption(screen.getByLabelText('Endpoint of model 1'), '(none)');
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'model', 0, 'endpoint'], undefined)
    );
  });

  it('shows the contradiction of a row under its endpoint', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const message =
      'key "endpoint" and key "base_url" contradict each other; an endpoint holds its own base URL';
    setup(
      chat({
        model: [{ name: 'm', endpoint: 'cle1', base_url: 'https://x.test' }],
      }),
      { fieldIssues: { 'with.model[0].endpoint': [error(message)] } }
    );
    expect(screen.getByText(message)).toBeInTheDocument();
  });

  it('has no endpoint field for the step, and a new row of the old method gets a provider', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const { onEdit } = setup(chat({ model: [{ name: 'a' }, { name: 'b' }] }));
    const provider = screen.getByLabelText('Provider of model 1');
    expect(provider).toHaveValue('');
    expect(within(provider).queryByText('(endpoint of the step)')).toBeNull();
    expect(screen.queryByLabelText('Endpoint')).toBeNull();
    expect(screen.queryByLabelText('Effort')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Add a model' }));
    expect(onEdit).toHaveBeenCalledWith(
      field(
        ['with', 'model'],
        [{ name: 'a' }, { name: 'b' }, { provider: 'openai', name: '' }]
      )
    );
  });

  it('adds a row with the endpoint of the last row', () => {
    mocks.choices = { choices: [choice('cle1')] };
    const { onEdit } = setup(
      chat({ model: [{ name: 'a', endpoint: 'cle1' }] })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Add a model' }));
    expect(onEdit).toHaveBeenCalledWith(
      field(
        ['with', 'model'],
        [
          { name: 'a', endpoint: 'cle1' },
          { endpoint: 'cle1', name: '' },
        ]
      )
    );
  });

  it('adds an openai row after a row of the old method, as before', () => {
    const { onEdit } = setup(chat({ model: [{ name: 'a' }] }));
    fireEvent.click(screen.getByRole('button', { name: 'Add a model' }));
    expect(onEdit).toHaveBeenCalledWith(
      field(
        ['with', 'model'],
        [{ name: 'a' }, { provider: 'openai', name: '' }]
      )
    );
  });

  it('draws no endpoint select for a row when no endpoint is available', () => {
    mocks.choices = { choices: [] };
    setup(chat({ model: [{ name: 'a', provider: 'openai' }] }));
    expect(screen.queryByLabelText('Endpoint of model 1')).toBeNull();
    expect(screen.getByLabelText('Provider of model 1')).toHaveValue('openai');
  });
});
