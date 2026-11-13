// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StepInspectorProps } from '../../contracts';
import { makeModel, makeStep } from '../../canvas/__tests__/fixtures';
import { catalogSample } from '../../testing/fixtures';
import { StepInspector } from '../StepInspector';
import { agentRunSchema } from './agentRunSchema';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));
const mocks = vi.hoisted(() => ({
  /** The answer of the route of endpoint choices; undefined while it loads. */
  choices: undefined as unknown,
  /** True for a signed-in administrator. */
  admin: false,
}));
vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) =>
    path === '/ai/endpoint-choices' && init !== null
      ? { data: mocks.choices }
      : { data: undefined },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/contexts/AuthContext', () => ({
  useIsAdminIfSignedIn: () => mocks.admin,
}));

afterEach(() => {
  vi.clearAllMocks();
  mocks.choices = undefined;
  mocks.admin = false;
});

const dag = { name: 'fix-bug' };
const MODELS = ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5'];

const CLI_ALIASES = ['sonnet', 'opus', 'haiku', 'opusplan'];

const choice = (name: string, models?: string[]) => ({
  name,
  protocol: 'anthropic',
  preset: 'anthropic',
  scope: 'global',
  ...(models ? { models } : {}),
});

/** The answer of the route of choices of a server that makes the model check. */
const answer = (...choices: ReturnType<typeof choice>[]) => ({
  choices,
  cliModelAliases: CLI_ALIASES,
});

const modelEdit = (value: unknown) => ({
  kind: 'setStepField',
  step: 'steps[0]',
  path: ['with', 'endpoint', 'model'],
  value,
});

function setup(
  withValue: Record<string, unknown>,
  onEdit: StepInspectorProps['onEdit'] = vi.fn(),
  readOnly = false
) {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      kind: 'action',
      action: 'agent.run',
      value: { action: 'agent.run', with: withValue },
    }),
  ]);
  return render(
    <StepInspector
      model={model}
      step="steps[0]"
      schema={agentRunSchema}
      catalog={catalogSample}
      customActions={[]}
      candidates={[]}
      issues={[]}
      readOnly={readOnly}
      dag={dag}
      onEdit={onEdit}
      onClose={() => {}}
    />
  );
}

const modelField = () => screen.getByLabelText('Model');

describe('Model suggestions of an agent step', () => {
  it('offers the models of the chosen endpoint as suggestions', () => {
    mocks.choices = answer(choice('cle1', MODELS));
    setup({ provider: 'claude', endpoint: { name: 'cle1' } });
    fireEvent.focus(modelField());
    const list = screen.getByRole('listbox', {
      name: 'Models of the endpoint',
    });
    expect(
      within(list)
        .getAllByRole('option')
        .map((option) => option.textContent)
    ).toEqual(MODELS);
  });

  it('writes a model that the user picks', () => {
    mocks.choices = answer(choice('cle1', MODELS));
    const onEdit = vi.fn();
    setup({ provider: 'claude', endpoint: { name: 'cle1' } }, onEdit);
    fireEvent.focus(modelField());
    fireEvent.click(screen.getByRole('option', { name: 'claude-haiku-5' }));
    fireEvent.blur(modelField());
    expect(onEdit).toHaveBeenCalledWith(modelEdit('claude-haiku-5'));
  });

  it('accepts free text, a reference and an alias of the CLI, and says in a muted line that the step fails with a name that is not in the list', () => {
    mocks.choices = answer(choice('cle1', MODELS));
    const onEdit = vi.fn();
    setup(
      { provider: 'claude', endpoint: { name: 'cle1', model: 'sonnet' } },
      onEdit
    );

    fireEvent.change(modelField(), { target: { value: 'my-fine-tune' } });
    fireEvent.blur(modelField());
    expect(onEdit).toHaveBeenLastCalledWith(modelEdit('my-fine-tune'));
    expect(screen.getByTestId('ve-model-not-listed')).toHaveTextContent(
      'Not in the list of this endpoint. The step fails when it starts, unless a refresh of the list finds it.'
    );
    // The line is a hint, not an issue: nothing is marked invalid.
    expect(modelField()).not.toHaveAttribute('aria-invalid');
    expect(screen.queryByRole('alert')).toBeNull();

    fireEvent.change(modelField(), { target: { value: '${params.MODEL}' } });
    fireEvent.blur(modelField());
    expect(onEdit).toHaveBeenLastCalledWith(modelEdit('${params.MODEL}'));
    expect(screen.queryByTestId('ve-model-not-listed')).toBeNull();

    fireEvent.change(modelField(), { target: { value: 'claude-opus-5-5' } });
    expect(screen.queryByTestId('ve-model-not-listed')).toBeNull();

    // The CLI claude accepts its aliases, which the list does not hold.
    for (const alias of ['sonnet', 'opusplan', 'sonnet[1m]']) {
      fireEvent.change(modelField(), { target: { value: alias } });
      expect(screen.queryByTestId('ve-model-not-listed')).toBeNull();
    }
    fireEvent.change(modelField(), {
      target: { value: 'claude-opus-5-5[1m]' },
    });
    expect(screen.queryByTestId('ve-model-not-listed')).toBeNull();
  });

  it('makes no claim when the server sends no aliases: it makes no model check', () => {
    mocks.choices = { choices: [choice('cle1', MODELS)] };
    setup({
      provider: 'claude',
      endpoint: { name: 'cle1', model: 'my-fine-tune' },
    });
    expect(screen.queryByTestId('ve-model-not-listed')).toBeNull();
  });

  it('shows a muted line for an endpoint with no list, with a link only for an administrator', () => {
    mocks.choices = answer(choice('cle1', []));
    const { unmount } = setup({
      provider: 'claude',
      endpoint: { name: 'cle1' },
    });
    expect(screen.getByTestId('ve-model-no-list')).toHaveTextContent(
      'This endpoint has no model list. Type the name of the model.'
    );
    fireEvent.focus(modelField());
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.queryByTestId('ve-open-text-link')).toBeNull();
    unmount();

    mocks.admin = true;
    setup({ provider: 'claude', endpoint: { name: 'cle1' } });
    expect(screen.getByTestId('ve-open-text-link')).toHaveTextContent(
      'Reload the list on the AI page'
    );
  });

  it('treats a missing models field of an older server as an empty list', () => {
    mocks.choices = answer(choice('cle1'));
    setup({ provider: 'claude', endpoint: { name: 'cle1' } });
    expect(screen.getByTestId('ve-model-no-list')).toBeInTheDocument();
  });

  it('claims nothing before the endpoints have arrived', () => {
    mocks.choices = undefined;
    setup({ provider: 'claude', endpoint: { name: 'cle1' } });
    expect(screen.queryByTestId('ve-model-no-list')).toBeNull();
    expect(screen.queryByTestId('ve-model-not-listed')).toBeNull();
  });

  it('changes nothing for a step with no endpoint', () => {
    mocks.choices = answer(choice('cle1', MODELS));
    const onEdit = vi.fn();
    setup({ provider: 'claude' }, onEdit);
    fireEvent.focus(modelField());
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.queryByTestId('ve-model-no-list')).toBeNull();
    expect(screen.queryByTestId('ve-model-not-listed')).toBeNull();
    fireEvent.change(modelField(), { target: { value: 'anything' } });
    fireEvent.blur(modelField());
    expect(onEdit).toHaveBeenCalledWith(modelEdit('anything'));
  });

  it('shows no suggestion for an endpoint that the DAG may not use', () => {
    mocks.choices = answer(choice('cle1', MODELS));
    setup({ provider: 'claude', endpoint: { name: 'typed-in-yaml' } });
    fireEvent.focus(modelField());
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.queryByTestId('ve-model-no-list')).toBeNull();
  });

  it('shows no suggestion in a run view', () => {
    mocks.choices = answer(choice('cle1', MODELS));
    setup({ provider: 'claude', endpoint: { name: 'cle1' } }, vi.fn(), true);
    fireEvent.focus(modelField());
    expect(screen.queryByRole('listbox')).toBeNull();
  });
});
