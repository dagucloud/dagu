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
import type { StepInspectorProps } from '../../contracts';
import { makeModel, makeStep } from '../../canvas/__tests__/fixtures';
import { catalogSample } from '../../testing/fixtures';
import {
  segmentOfSection,
  sectionOfPath,
  StepInspector,
} from '../StepInspector';
import { getActionWithSchema } from '../withSchema';
import { AGENT_RUN_KEYS, agentRunSchema } from './agentRunSchema';
import { realSchema } from './realSchema';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));
const mocks = vi.hoisted(() => ({
  /** The answer of the route of endpoint choices; undefined while it loads. */
  choices: undefined as unknown,
  /** The last query that the form sent for the choices. */
  choicesInit: undefined as unknown,
}));
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

afterEach(() => {
  vi.clearAllMocks();
  mocks.choices = undefined;
  mocks.choicesInit = undefined;
});

function setup(
  withValue: Record<string, unknown>,
  extra: Record<string, unknown> = {},
  candidates: StepInspectorProps['candidates'] = [],
  issues: StepInspectorProps['issues'] = [],
  serverStep?: StepInspectorProps['serverStep'],
  onEdit: StepInspectorProps['onEdit'] = () => {},
  readOnly = false,
  more: Partial<StepInspectorProps> = {}
) {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      kind: 'action',
      action: 'agent.run',
      value: { action: 'agent.run', with: withValue, ...extra },
    }),
  ]);
  return render(
    <StepInspector
      model={model}
      step="steps[0]"
      schema={agentRunSchema}
      catalog={catalogSample}
      customActions={[]}
      candidates={candidates}
      issues={issues}
      serverStep={serverStep}
      readOnly={readOnly}
      onEdit={onEdit}
      onClose={() => {}}
      {...more}
    />
  );
}

/** The keys of `with` that the form shows, from the field boxes. */
function shownKeys(container: HTMLElement): string[] {
  const keys = new Set<string>();
  for (const box of container.querySelectorAll<HTMLElement>(
    '[data-field-path^="with."]'
  )) {
    const key = /^with\.([^.[]+)/.exec(box.dataset.fieldPath ?? '')?.[1];
    if (key) keys.add(key);
  }
  return [...keys];
}

describe('agent.run in the step inspector', () => {
  it('shows a field for each key of the spec, and no engine key', () => {
    const { container } = setup({ provider: 'claude' });
    expect(shownKeys(container).sort()).toEqual([...AGENT_RUN_KEYS].sort());
    for (const removed of ['engine', 'fallback', 'cli', 'environment']) {
      expect(shownKeys(container)).not.toContain(removed);
    }
  });

  // A key that the schema does not list keeps its place: the editor says why and does not delete it.
  it('keeps a key that the schema does not list, read-only, with the text of its validation issue', () => {
    const { container } = setup(
      { provider: 'claude', custom: 'x', max_tool_iterations: 7 },
      {},
      [],
      [
        {
          severity: ValidationIssueSeverity.error,
          source: ValidationIssueSource.build,
          message: 'key "custom" is not supported',
          stepPath: 'steps[0]',
          fieldPath: 'steps[0].with.custom',
        },
      ]
    );
    expect(shownKeys(container)).toContain('max_tool_iterations');
    expect(shownKeys(container)).not.toContain('custom');
    const other = screen.getByTestId('ve-other-keys');
    expect(within(other).getByText('custom')).toBeInTheDocument();
    expect(within(other).getByRole('alert')).toHaveTextContent(
      'key "custom" is not supported'
    );
    // The issue counts in no section.
    const limits = Array.from(
      container.querySelectorAll('section > button')
    ).find((button) => button.textContent?.startsWith('Limits'));
    expect(limits?.textContent).toBe('Limits');
  });
});

describe('Security section of a step that runs a process, in a DAG with an agent step', () => {
  function setupRun(extra: Record<string, unknown> = {}) {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'agent.run',
        value: { action: 'agent.run', with: { provider: 'claude' } },
      }),
      makeStep({
        key: 'steps[1]',
        kind: 'run',
        runPreview: './scripts/test.sh',
        value: { run: './scripts/test.sh', ...extra },
      }),
    ]);
    return render(
      <StepInspector
        model={model}
        step="steps[1]"
        schema={agentRunSchema}
        catalog={catalogSample}
        customActions={[]}
        candidates={[]}
        issues={[]}
        readOnly={false}
        onEdit={() => {}}
        onClose={() => {}}
      />
    );
  }

  it('shows the step as untrusted by default', () => {
    setupRun();
    expect(screen.getByLabelText('Untrusted')).toBeChecked();
    expect(
      screen.getByText('Untrusted by default: the DAG has an agent step.')
    ).toBeInTheDocument();
  });

  it('shows the step as trusted with untrusted: false', () => {
    setupRun({ untrusted: false });
    expect(screen.getByLabelText('Untrusted')).not.toBeChecked();
    expect(
      screen.getByText('Trusted: the key untrusted is false.')
    ).toBeInTheDocument();
  });
});

describe('Security section in the step inspector', () => {
  it('opens after the Flow section, with no mark for the default trust', () => {
    setup({ provider: 'claude' });
    const heads = Array.from(document.querySelectorAll('section > button')).map(
      (button) => button.textContent
    );
    expect(heads.indexOf('Security')).toBeGreaterThan(heads.indexOf('Flow'));
    expect(screen.queryByTestId('untrusted-badge')).toBeNull();
  });

  it('explains that an agent step is untrusted, with no list of secrets', () => {
    setup({ provider: 'codex' });
    expect(screen.getByRole('button', { name: 'Security' })).toBeVisible();
    expect(
      screen.getByText('A step with a CLI provider is always untrusted.')
    ).toBeInTheDocument();
    expect(screen.queryByLabelText('Add a secret')).toBeNull();
  });
});

function headers(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('section > button')).map(
    (button) => button.textContent ?? ''
  );
}

const FORM = [
  'Model',
  'System message',
  'Prompt',
  'Tools',
  'Skills',
  'Limits',
  'Output',
];

const field = (path: (string | number)[], value: unknown) => ({
  kind: 'setStepField',
  step: 'steps[0]',
  path,
  value,
});

/** The step `code` of examples/agent-worker-demo/dags/agent-dev-real.yaml. */
const REAL_CODE = {
  provider: 'claude',
  endpoint: { name: 'claude-sub', model: 'claude-haiku-4-5' },
  prompt: 'A test of this Python project fails. Fix it.',
  system: 'Never edit a test file.',
  max_tool_iterations: 12,
  tools: {
    builtin: {
      read: true,
      edit: true,
      search: true,
      shell: {
        allow: ['./scripts/test.sh', 'git diff *', 'git status *'],
        deny: ['git push *', 'git remote *'],
      },
    },
  },
};

describe('the Skills section of an agent step', () => {
  it('lists the skills of with.skills, and removes one with an editStepSkills edit', () => {
    const onEdit = vi.fn();
    setup(
      { provider: 'claude', prompt: 'x', skills: ['code-style', 'review'] },
      {},
      [],
      [],
      undefined,
      onEdit
    );
    const entries = screen.getAllByTestId('ve-skill-entry');
    expect(entries).toHaveLength(2);
    expect(entries[0]).toHaveTextContent('code-style');
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove skill review' })
    );
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'editStepSkills',
      step: 'steps[0]',
      change: { kind: 'remove', index: 1 },
    });
  });

  it('is after the Tools section and before the Limits section', () => {
    const { container } = setup({
      provider: 'claude',
      prompt: 'x',
      skills: ['code-style'],
    });
    const heads = headers(container);
    const at = (title: string) =>
      heads.findIndex((text) => text.startsWith(title));
    expect(at('Skills')).toBeGreaterThan(at('Tools'));
    expect(at('Skills')).toBeLessThan(at('Limits'));
  });

  it('says that the CLI reads a skill with its tool Skill, not with load_skill', () => {
    setup({ provider: 'claude', prompt: 'x', skills: ['code-style'] });
    expect(
      screen.getByText('The CLI reads a skill with its tool Skill.')
    ).toBeInTheDocument();
    expect(screen.queryByText(/load_skill/)).toBeNull();
  });

  it('shows no key `skills` in the Other keys list', () => {
    setup({ provider: 'claude', prompt: 'x', skills: ['a'], custom: 1 });
    const other = screen.getByTestId('ve-other-keys');
    expect(within(other).queryByText('skills')).toBeNull();
    expect(within(other).getByText('custom')).toBeInTheDocument();
  });
});

describe('the sections of an agent step', () => {
  it('has the sections of the chat form where the keys are the same, and no Parameters form', () => {
    const { container } = setup({ provider: 'claude', prompt: 'x' });
    expect(headers(container).filter((text) => FORM.includes(text))).toEqual(
      FORM
    );
    expect(headers(container)).not.toContain('Parameters');
  });

  it('starts every section open', () => {
    setup({ provider: 'claude', prompt: 'x' });
    for (const name of FORM) {
      expect(screen.getByRole('button', { name })).toHaveAttribute(
        'aria-expanded',
        'true'
      );
    }
  });

  it('keeps every other key of `with` in an Other keys list', () => {
    setup({ provider: 'claude', prompt: 'x', custom_flag: { a: 1 } });
    const other = screen.getByTestId('ve-other-keys');
    expect(within(other).getByText('custom_flag')).toBeInTheDocument();
    expect(within(other).queryByText('prompt')).toBeNull();
  });

  it('has no Messages toggle: messages is not a key of agent.run', () => {
    setup({ provider: 'claude', prompt: 'x' });
    expect(screen.queryByRole('button', { name: 'Messages' })).toBeNull();
    expect(screen.getByLabelText('Prompt')).toHaveValue('x');
  });

  it('shows the timeout in Limits only, not in Flow', () => {
    setup({ provider: 'claude' }, { timeout_sec: 600 });
    expect(screen.getAllByLabelText('Timeout (seconds)')).toHaveLength(1);
    expect(screen.getByLabelText('Timeout (seconds)')).toHaveValue('600');
  });
});

describe('Model section of an agent step', () => {
  it('lists the CLI providers only, and writes the choice', () => {
    const onEdit = vi.fn();
    setup({ provider: 'claude' }, {}, [], [], undefined, onEdit);
    const select = screen.getByLabelText('Provider');
    for (const name of ['claude', 'codex', 'gemini', 'opencode']) {
      expect(within(select).getByRole('option', { name })).toBeInTheDocument();
    }
    for (const name of ['anthropic', 'openai', 'local']) {
      expect(within(select).queryByRole('option', { name })).toBeNull();
    }
    fireEvent.change(select, { target: { value: 'codex' } });
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'provider'], 'codex'));
  });

  it('keeps a chat provider as a text value, not as a choice of the list', () => {
    setup({ provider: 'anthropic' });
    expect(screen.queryByRole('combobox', { name: 'Provider' })).toBeNull();
    expect(screen.getByLabelText('Provider')).toHaveValue('anthropic');
  });

  it('writes the model on blur and removes the key when it is emptied', () => {
    const onEdit = vi.fn();
    setup(
      { provider: 'claude', endpoint: { model: 'haiku' } },
      {},
      [],
      [],
      undefined,
      onEdit
    );
    const model = screen.getByLabelText('Model');
    fireEvent.change(model, { target: { value: 'sonnet' } });
    fireEvent.blur(model);
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'endpoint', 'model'], 'sonnet')
    );
    fireEvent.change(model, { target: { value: '' } });
    fireEvent.blur(model);
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'endpoint', 'model'], undefined)
    );
  });

  it('marks the provider and the model as required', () => {
    const { container } = setup({ provider: 'claude' });
    const provider = container.querySelector<HTMLElement>(
      '[data-field-path="with.provider"]'
    );
    expect(provider).toHaveAttribute('data-required', 'true');
    expect(provider).not.toHaveAttribute('data-empty');
    // The model is required too: the server refuses a step with none.
    expect(
      container.querySelector('[data-field-path="with.endpoint.model"]')
    ).toHaveAttribute('data-required', 'true');
  });

  it('tells that the provider is empty and required for a step with no provider', () => {
    const { container } = setup({});
    const provider = container.querySelector<HTMLElement>(
      '[data-field-path="with.provider"]'
    );
    expect(provider).toHaveAttribute('data-required', 'true');
    expect(provider).toHaveAttribute('data-empty', 'true');
    expect(screen.getByLabelText('Provider')).toHaveValue('');
    expect(
      screen.getByText('Required. The CLI that runs the step.')
    ).toBeVisible();
  });

  it('shows the values of the step only, not the ones of the compiled step', () => {
    setup({ prompt: 'x' }, {}, [], [], {
      name: 'review',
      executorConfig: {
        type: 'harness',
        config: { provider: 'codex', model: 'gpt-5' },
      },
    });
    expect(screen.getByLabelText('Provider')).toHaveValue('');
    expect(screen.getByLabelText('Model')).toHaveValue('');
  });
});

describe('Endpoint choice of an agent step', () => {
  const choice = (name: string, protocol = 'anthropic') => ({
    name,
    protocol,
    preset: protocol === 'anthropic' ? 'anthropic' : 'mistral',
    scope: 'global',
  });
  const dag = { name: 'fix-bug', workspace: 'team' };

  it('lists the endpoints of the protocol anthropic for the DAG, and writes the choice', () => {
    mocks.choices = { choices: [choice('cle1'), choice('mistral1', 'openai')] };
    const onEdit = vi.fn();
    setup({ provider: 'claude' }, {}, [], [], undefined, onEdit, false, {
      dag,
    });

    // The form asks for the endpoints of this DAG in its workspace.
    expect(mocks.choicesInit).toEqual({
      params: {
        query: { remoteNode: 'local', dag: 'fix-bug', workspace: 'team' },
      },
    });
    const select = screen.getByLabelText('Endpoint');
    // The endpoint is required: no "(none)" item.
    expect(optionNames(select)).toEqual(['cle1']);
    pickOption(select, 'cle1');
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'endpoint', 'name'], 'cle1')
    );
  });

  it('comes before the Model field, and the picker has no way to remove the key', () => {
    mocks.choices = { choices: [choice('cle1'), choice('cle2')] };
    const onEdit = vi.fn();
    const { container } = setup(
      { provider: 'claude', endpoint: { name: 'cle1', model: 'sonnet' } },
      {},
      [],
      [],
      undefined,
      onEdit,
      false,
      { dag }
    );
    const boxes = [
      ...container.querySelectorAll<HTMLElement>('[data-field-path]'),
    ].map((box) => box.dataset.fieldPath);
    expect(boxes.indexOf('with.endpoint.name')).toBeGreaterThan(
      boxes.indexOf('with.provider')
    );
    expect(boxes.indexOf('with.endpoint.name')).toBeLessThan(
      boxes.indexOf('with.endpoint.model')
    );
    expect(screen.getByLabelText('Endpoint')).toHaveTextContent('cle1');
    // The Model stays free text.
    expect(screen.getByLabelText('Model')).toHaveValue('sonnet');
    expect(optionNames(screen.getByLabelText('Endpoint'))).toEqual([
      'cle1',
      'cle2',
    ]);
    pickOption(screen.getByLabelText('Endpoint'), 'cle2');
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'endpoint', 'name'], 'cle2')
    );
  });

  it('shows an error under the Endpoint while the step names none, and asks to select one', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup({ provider: 'claude' }, {}, [], [], undefined, vi.fn(), false, {
      dag,
    });
    expect(screen.getByLabelText('Endpoint')).toHaveTextContent(
      'Select an endpoint'
    );
    expect(screen.getByRole('alert')).toHaveTextContent(
      'agent.run needs an endpoint; add one on the AI page and name it here'
    );
    expect(
      screen.getByText('Required. The step uses the key of the endpoint.')
    ).toBeInTheDocument();
  });

  it('asks for the name of the endpoint when the block has none', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(
      { provider: 'claude', endpoint: { model: 'sonnet' } },
      {},
      [],
      [],
      undefined,
      vi.fn(),
      false,
      { dag }
    );
    expect(screen.getByRole('alert')).toHaveTextContent(
      'agent.run needs the name of an endpoint'
    );
  });

  it('removes a model that the new endpoint does not list, in the same edit', () => {
    mocks.choices = {
      choices: [
        { ...choice('cle1'), models: ['claude-haiku-5'] },
        { ...choice('cle2'), models: ['claude-opus-5-5'] },
      ],
      cliModelAliases: ['sonnet'],
    };
    const onEdit = vi.fn();
    setup(
      {
        provider: 'claude',
        endpoint: { name: 'cle1', model: 'claude-haiku-5' },
      },
      {},
      [],
      [],
      undefined,
      onEdit,
      false,
      { dag }
    );
    pickOption(screen.getByLabelText('Endpoint'), 'cle2');
    expect(onEdit.mock.calls.map(([op]) => op)).toEqual([
      field(['with', 'endpoint', 'name'], 'cle2'),
      field(['with', 'endpoint', 'model'], undefined),
    ]);
  });

  // A text endpoint is a value of the wrong type, and `model` and `effort`
  // are no keys of the `with` of an agent step: the form edits none of them.
  it('shows a text endpoint as YAML only, and model and effort with the other keys', () => {
    mocks.choices = { choices: [choice('cle1'), choice('cle2')] };
    setup(
      { provider: 'claude', endpoint: 'cle1', model: 'sonnet', effort: 'low' },
      {},
      [],
      [],
      undefined,
      vi.fn(),
      false,
      { dag }
    );
    expect(screen.queryByLabelText('Endpoint')).toBeNull();
    expect(screen.queryByLabelText('Model')).toBeNull();
    expect(screen.getByTestId('ve-yaml-only')).toHaveTextContent(
      'endpoint: "cle1"'
    );
    const other = screen.getByTestId('ve-other-keys');
    expect(within(other).getByText('model')).toBeInTheDocument();
    expect(within(other).getByText('effort')).toBeInTheDocument();
  });

  it('shows a model error while the step names an endpoint and no model', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(
      { provider: 'claude', endpoint: { name: 'cle1' } },
      {},
      [],
      [],
      undefined,
      vi.fn(),
      false,
      { dag }
    );
    expect(screen.getByRole('alert')).toHaveTextContent(
      'agent.run needs a model; choose one that the endpoint lists'
    );
  });

  it('shows no endpoint error once the step names one', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(
      { provider: 'claude', endpoint: { name: 'cle1', model: 'sonnet' } },
      {},
      [],
      [],
      undefined,
      vi.fn(),
      false,
      { dag }
    );
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows a muted hint with a link to the AI page when no endpoint is available', () => {
    mocks.choices = { choices: [choice('mistral1', 'openai')] };
    setup({ provider: 'claude' }, {}, [], [], undefined, vi.fn(), false, {
      dag,
    });

    expect(screen.queryByLabelText('Endpoint')).toBeNull();
    const hint = screen.getByTestId('ve-endpoint-empty');
    expect(hint).toHaveTextContent('No endpoint is available for this DAG.');
    expect(within(hint).getByRole('link')).toHaveAttribute(
      'href',
      '/ai/endpoints'
    );
  });

  it('keeps a name that the list does not hold as a value, with a warning', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(
      { provider: 'claude', endpoint: { name: 'typed-in-yaml' } },
      {},
      [],
      [],
      undefined,
      vi.fn(),
      false,
      { dag }
    );

    expect(screen.getByLabelText('Endpoint')).toHaveTextContent(
      'typed-in-yaml'
    );
    expect(
      screen.getByText(/The server does not know the endpoint "typed-in-yaml"/)
    ).toBeVisible();
  });

  it('warns about an endpoint with another protocol', () => {
    mocks.choices = { choices: [choice('mistral1', 'openai')] };
    setup(
      { provider: 'claude', endpoint: { name: 'mistral1' } },
      {},
      [],
      [],
      undefined,
      vi.fn(),
      false,
      { dag }
    );
    expect(screen.getByLabelText('Endpoint')).toHaveTextContent('mistral1');
    expect(
      screen.getByText(/does not have the protocol anthropic/)
    ).toBeVisible();
  });

  it('claims nothing while the list loads or when it is not available', () => {
    setup(
      { provider: 'claude', endpoint: { name: 'cle1' } },
      {},
      [],
      [],
      undefined,
      vi.fn(),
      false,
      {
        dag,
      }
    );
    expect(screen.getByLabelText('Endpoint')).toHaveTextContent('cle1');
    expect(screen.queryByText(/does not know the endpoint/)).toBeNull();
    expect(screen.queryByTestId('ve-endpoint-empty')).toBeNull();
  });

  it('is disabled in a run view', () => {
    mocks.choices = { choices: [choice('cle1')] };
    setup(
      { provider: 'claude', endpoint: { name: 'cle1' } },
      {},
      [],
      [],
      undefined,
      vi.fn(),
      true,
      {
        dag,
      }
    );
    expect(screen.getByLabelText('Endpoint')).toBeDisabled();
  });

  it('shows no endpoint request for a DAG with no name', () => {
    setup({ provider: 'claude' }, {}, [], [], undefined, vi.fn(), false, {
      dag: { name: '' },
    });
    expect(mocks.choicesInit).toBeNull();
  });
});

describe('Tools section of an agent step', () => {
  it('shows the built-in tools, with the shell patterns', () => {
    setup(REAL_CODE);
    const block = screen.getByTestId('ve-builtin-tools');
    expect(within(block).getByLabelText('Read files')).toBeChecked();
    expect(within(block).getByLabelText('Write files')).not.toBeChecked();
    expect(within(block).getByLabelText('Edit files')).toBeChecked();
    expect(within(block).getByLabelText('Search files')).toBeChecked();
    for (const pattern of [
      './scripts/test.sh',
      'git diff *',
      'git push *',
      'git remote *',
    ]) {
      expect(within(block).getByText(pattern)).toBeInTheDocument();
    }
    // The tools are the built-in ones: the list of entries does not say that there are none.
    expect(screen.queryByText('No tools yet')).toBeNull();
  });

  it('shows the built-in tools, all off, for a step that has none yet', () => {
    setup({ provider: 'claude' });
    const block = screen.getByTestId('ve-builtin-tools');
    expect(within(block).getByLabelText('Read files')).not.toBeChecked();
    expect(within(block).getByText('Shell commands')).toBeInTheDocument();
  });

  it('writes a switch as true and removes the key when it goes off', () => {
    const onEdit = vi.fn();
    setup(REAL_CODE, {}, [], [], undefined, onEdit);
    const block = screen.getByTestId('ve-builtin-tools');
    fireEvent.click(within(block).getByLabelText('Write files'));
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'tools', 'builtin', 'write'], true)
    );
    fireEvent.click(within(block).getByLabelText('Read files'));
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'tools', 'builtin', 'read'], undefined)
    );
  });

  it('adds and removes a shell pattern with the whole list', () => {
    const onEdit = vi.fn();
    setup(REAL_CODE, {}, [], [], undefined, onEdit);
    const block = screen.getByTestId('ve-builtin-tools');
    const [allowInput] = within(block).getAllByLabelText('New item');
    fireEvent.change(allowInput!, { target: { value: 'make *' } });
    fireEvent.keyDown(allowInput!, { key: 'Enter' });
    expect(onEdit).toHaveBeenCalledWith(
      field(
        ['with', 'tools', 'builtin', 'shell', 'allow'],
        ['./scripts/test.sh', 'git diff *', 'git status *', 'make *']
      )
    );
    fireEvent.click(within(block).getByLabelText('Remove git push *'));
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'tools', 'builtin', 'shell', 'deny'], ['git remote *'])
    );
  });

  it('is read-only in a run view: the switches are disabled and no pattern has a control', () => {
    setup(REAL_CODE, {}, [], [], undefined, () => {}, true);
    const block = screen.getByTestId('ve-builtin-tools');
    expect(within(block).getByLabelText('Read files')).toBeDisabled();
    expect(within(block).queryByLabelText('New item')).toBeNull();
    expect(within(block).queryByLabelText('Remove git push *')).toBeNull();
  });

  it('does not show the two limits of the catalog: they are not keys of an agent step', () => {
    setup({ provider: 'claude' });
    expect(screen.queryByTestId('ve-tool-advanced')).toBeNull();
  });

  it('has no entry and no limit for a step with no DAG or MCP tool, and no add button without a host', () => {
    setup({ provider: 'claude' });
    expect(screen.queryByText('No tools yet')).toBeNull();
    expect(screen.queryByTestId('ve-tool-entry')).toBeNull();
    expect(screen.queryByTestId('ve-tool-advanced')).toBeNull();
    expect(screen.queryByText('Add a tool')).toBeNull();
  });

  // The agent step takes the tools of a chat step besides its built-in tools.
  it('lists the DAG and MCP tools of the step above its built-in tools', () => {
    const onOpenTool = vi.fn();
    setup(
      {
        provider: 'claude',
        dags: [{ name: 'lookup' }],
        tools: {
          mcp: [{ name: 'docs', url: 'https://docs.example.com/mcp' }],
          builtin: { read: true },
        },
      },
      {},
      [],
      [],
      undefined,
      () => {},
      false,
      { onOpenTool }
    );
    const rows = screen.getAllByTestId('ve-tool-entry');
    expect(rows.map((row) => row.getAttribute('data-kind'))).toEqual([
      'dag',
      'mcp',
    ]);
    expect(screen.getByTestId('ve-builtin-tools')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Edit tool lookup' }));
    expect(onOpenTool).toHaveBeenCalledWith({
      step: 'steps[0]',
      category: 'dags',
      index: 0,
    });
    // The list is not a second place for the limits of the chat step.
    expect(screen.queryByTestId('ve-tool-advanced')).toBeNull();
  });

  it('has an add button that opens the same panel of kinds as the canvas, and none in a run view', () => {
    const onAddTool = vi.fn();
    setup({ provider: 'claude' }, {}, [], [], undefined, () => {}, false, {
      onAddTool,
    });
    fireEvent.click(screen.getByTestId('ve-tool-add'));
    expect(onAddTool).toHaveBeenCalledTimes(1);
    expect(onAddTool).toHaveBeenLastCalledWith('tools');
    // The DAG tools have a dock of their own.
    fireEvent.click(screen.getByTestId('ve-dag-add'));
    expect(onAddTool).toHaveBeenLastCalledWith('dags');
  });

  it('shows the issue of a tool entry on its row, not as a read-only value', () => {
    setup(
      { provider: 'claude', dags: [{ name: 'lookup' }] },
      {},
      [],
      [
        {
          severity: ValidationIssueSeverity.error,
          source: ValidationIssueSource.build,
          message: 'dags[0] is not supported',
          stepPath: 'steps[0]',
          fieldPath: 'steps[0].with.dags[0]',
        },
      ]
    );
    const row = screen.getByTestId('ve-tool-entry');
    expect(row).toHaveTextContent('lookup');
    expect(
      within(row).getByLabelText('1 issue', { exact: false })
    ).toBeInTheDocument();
  });
});

// A double click on the Model node of the canvas.
describe('a request from the canvas to open a section', () => {
  function inspector(
    reveal?: { section: 'model'; seq: number },
    action = 'agent.run',
    withValue: Record<string, unknown> = {
      provider: 'claude',
      endpoint: { model: 'sonnet' },
    }
  ) {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action,
        value: { action, with: withValue },
      }),
    ]);
    const onRevealed = vi.fn();
    const element = (value?: { section: 'model'; seq: number }) => (
      <StepInspector
        model={model}
        step="steps[0]"
        schema={agentRunSchema}
        catalog={catalogSample}
        customActions={[]}
        candidates={[]}
        issues={[]}
        readOnly={false}
        onEdit={() => {}}
        onClose={() => {}}
        onRevealed={onRevealed}
        {...(value ? { reveal: value } : {})}
      />
    );
    const view = render(element(reveal));
    return { ...view, onRevealed, element };
  }

  const section = (name: string) =>
    screen.getByRole('button', { name: new RegExp(`^${name}`) });

  it('opens a closed Model section and focuses the model field', () => {
    const { rerender, element } = inspector();
    fireEvent.click(section('Model'));
    expect(section('Model')).toHaveAttribute('aria-expanded', 'false');
    rerender(element({ section: 'model', seq: 1 }));
    expect(section('Model')).toHaveAttribute('aria-expanded', 'true');
    expect(
      document.activeElement
        ?.closest('[data-field-path]')
        ?.getAttribute('data-field-path')
    ).toBe('with.endpoint.model');
  });

  // The field that takes the focus is the one that writes the model of the step.
  it.each([
    [{ endpoint: { name: 'cle1' } }, 'with.endpoint.model'],
    [{ prompt: 'hi' }, 'with.endpoint.model'],
    [{ provider: 'openai', model: 'gpt-5' }, 'with.model'],
  ])('focuses the model field of a chat step with %j', (withValue, path) => {
    inspector({ section: 'model', seq: 1 }, 'chat.completion', withValue);
    expect(
      document.activeElement
        ?.closest('[data-field-path]')
        ?.getAttribute('data-field-path')
    ).toBe(path);
  });

  it('works on a form that mounts with the request, and says that it did', () => {
    const { onRevealed } = inspector({ section: 'model', seq: 1 });
    expect(section('Model')).toHaveAttribute('aria-expanded', 'true');
    expect(onRevealed).toHaveBeenCalled();
  });
});

describe('Limits of an agent step', () => {
  it('writes the limits as numbers, and removes a key that is emptied', () => {
    const onEdit = vi.fn();
    setup(
      { provider: 'claude', max_tool_iterations: 12, max_tool_calls: 100 },
      {},
      [],
      [],
      undefined,
      onEdit
    );
    const turns = screen.getByLabelText('Maximum number of tool iterations');
    expect(turns).toHaveValue('12');
    fireEvent.change(turns, { target: { value: '20' } });
    fireEvent.blur(turns);
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'max_tool_iterations'], 20)
    );
    fireEvent.change(turns, { target: { value: '' } });
    fireEvent.blur(turns);
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'max_tool_iterations'], undefined)
    );
    const calls = screen.getByLabelText('Maximum number of tool calls');
    fireEvent.change(calls, { target: { value: '6' } });
    fireEvent.blur(calls);
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'max_tool_calls'], 6)
    );
    fireEvent.change(calls, { target: { value: '' } });
    fireEvent.blur(calls);
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'max_tool_calls'], undefined)
    );
    const timeout = screen.getByLabelText('Timeout (seconds)');
    fireEvent.change(timeout, { target: { value: '600' } });
    fireEvent.blur(timeout);
    expect(onEdit).toHaveBeenLastCalledWith(field(['timeout_sec'], 600));
  });

  it('keeps a number out of its range and says so', () => {
    const onEdit = vi.fn();
    setup({ provider: 'claude' }, {}, [], [], undefined, onEdit);
    const turns = screen.getByLabelText('Maximum number of tool iterations');
    fireEvent.change(turns, { target: { value: '0' } });
    fireEvent.blur(turns);
    expect(onEdit).not.toHaveBeenCalled();
    expect(screen.getByText('Enter a whole number, 1 or more')).toBeVisible();
    const calls = screen.getByLabelText('Maximum number of tool calls');
    fireEvent.change(calls, { target: { value: '0' } });
    fireEvent.blur(calls);
    expect(onEdit).not.toHaveBeenCalled();
    expect(screen.getAllByText('Enter a whole number, 1 or more')).toHaveLength(
      2
    );
  });

  it('shows the default of the tool calls, and the value that the file has', () => {
    const view = setup({ provider: 'claude' });
    const calls = screen.getByLabelText('Maximum number of tool calls');
    expect(calls).toHaveValue('');
    expect(calls).toHaveAttribute('placeholder', 'Default: 100');
    view.unmount();
    setup({ provider: 'claude', max_tool_calls: 6 });
    expect(screen.getByLabelText('Maximum number of tool calls')).toHaveValue(
      '6'
    );
  });
});

describe('issues of an agent step', () => {
  const issue = (fieldPath: string, message = 'bad value') => ({
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message,
    stepPath: 'steps[0]',
    fieldPath: `steps[0].${fieldPath}`,
  });

  it('counts the issues of each section like the chat form does', () => {
    const { container } = setup(
      { provider: 'claude' },
      {},
      [],
      [
        issue('with.max_tool_iterations'),
        issue('with.max_tool_calls'),
        issue('timeout_sec'),
        issue('with.provider'),
        issue('with.dags[0]'),
        issue('output_schema'),
        issue('with'),
      ]
    );
    const count = (title: string) =>
      Array.from(container.querySelectorAll('section > button'))
        .find((button) => button.textContent?.startsWith(title))
        ?.textContent?.slice(title.length);
    expect(count('Limits')).toBe('3');
    expect(count('Model')).toBe('1');
    expect(count('Tools')).toBe('1');
    expect(count('Output')).toBe('1');
    // An issue on `with` itself belongs to Prompt.
    expect(count('Prompt')).toBe('1');
    expect(count('Skills')).toBe('');
  });

  it('shows the text of the issue under its field', () => {
    setup(
      { provider: 'claude', endpoint: { name: 'cle1', model: 'sonnet' } },
      {},
      [],
      [
        issue(
          'with.max_tool_iterations',
          'max_tool_iterations must be at least 1'
        ),
      ]
    );
    expect(screen.getByRole('alert')).toHaveTextContent(
      'max_tool_iterations must be at least 1'
    );
  });
});

describe('sectionOfPath for an agent step', () => {
  it.each([
    ['with.provider', 'model'],
    ['with.endpoint', 'model'],
    ['with.endpoint.name', 'model'],
    ['with.endpoint.model', 'model'],
    ['with.endpoint.effort', 'model'],
    ['with.system', 'system'],
    ['with.prompt', 'prompt'],
    ['with', 'prompt'],
    ['with.tools', 'tools'],
    ['with.tools.builtin.shell.allow[0]', 'tools'],
    ['with.dags[1].name', 'tools'],
    ['with.tools.mcp[1].url', 'tools'],
    ['with.skills[0]', 'skills'],
    ['with.max_tool_iterations', 'limits'],
    ['with.max_tool_calls', 'limits'],
    ['timeout_sec', 'limits'],
    ['output_schema', 'output'],
    ['secrets', 'security'],
    ['untrusted', 'security'],
    ['retry_policy.limit', 'flow'],
    ['preconditions[0]', 'flow'],
  ])('%s opens %s', (path, section) => {
    expect(sectionOfPath(path, 'agentRun')).toBe(section);
  });

  it('gives an unknown key no section', () => {
    for (const path of [
      'with.custom',
      'with.agent',
      'with.model',
      'with.effort',
      'with.api.base_url',
      // The two limits of the catalog are keys of a chat step only.
      'with.max_tools',
      // A key of the prototype of an object is no key of the form.
      'with.constructor',
    ]) {
      expect(sectionOfPath(path, 'agentRun'), path).toBeNull();
    }
  });

  // The schema file of the repository lists these keys, and the form edits each one.
  it('has a section for each key that the form edits, and the schema lists them', () => {
    const schema = realSchema;
    if (!schema) return;
    const listed = Object.keys(
      getActionWithSchema(schema, 'agent.run', [])?.properties ?? {}
    );
    for (const key of AGENT_RUN_KEYS) {
      expect(listed, key).toContain(key);
      expect(sectionOfPath(`with.${key}`, 'agentRun'), key).not.toBeNull();
    }
  });

  it('keeps the generic sections for the other steps', () => {
    expect(sectionOfPath('with.max_tool_iterations')).toBe('parameters');
    expect(sectionOfPath('timeout_sec')).toBe('flow');
  });
});

describe('sections of the step keys', () => {
  it('places secrets and untrusted in the Security section of the Settings segment', () => {
    for (const path of ['secrets', 'secrets[0]', 'untrusted']) {
      expect(sectionOfPath(path)).toBe('security');
    }
    expect(segmentOfSection('security')).toBe('settings');
    expect(segmentOfSection('flow')).toBe('settings');
    expect(segmentOfSection('parameters')).toBe('parameters');
  });
});
