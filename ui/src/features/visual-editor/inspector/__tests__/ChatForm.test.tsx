// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import type { ComponentType } from 'react';
import { describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type {
  DagEditOp,
  ReferenceCandidate,
  StepView,
  ValidationIssue,
} from '../../contracts';
import { applyEdit } from '../../doc';
import { makeStep } from '../../canvas/__tests__/fixtures';
import {
  ModelSection,
  type ChatSectionProps,
} from '../sections/chat/ModelSection';
import { OptionsSection } from '../sections/chat/OptionsSection';
import { OutputSection, STARTER_SCHEMA } from '../sections/chat/OutputSection';
import { PromptSection, SystemSection } from '../sections/chat/PromptSections';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ data: undefined }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const candidates: ReferenceCandidate[] = [
  {
    token: '${steps.fetch.outputs.body}',
    label: 'fetch body',
    group: 'steps',
    stepName: 'fetch',
    distance: 1,
    known: 'runtime',
  },
];

function chat(
  withValue: Record<string, unknown> | undefined,
  extra: Record<string, unknown> = {}
): StepView {
  return makeStep({
    key: 'steps[0]',
    kind: 'action',
    action: 'chat.completion',
    value: {
      action: 'chat.completion',
      ...(withValue ? { with: withValue } : {}),
      ...extra,
    },
  });
}

function setup(
  Section: ComponentType<ChatSectionProps>,
  step: StepView,
  options: {
    readOnly?: boolean;
    fieldIssues?: Record<string, ValidationIssue[]>;
  } = {}
) {
  const onEdit = vi.fn<(op: DagEditOp) => void>();
  const view = render(
    <Section
      step={step}
      readOnly={options.readOnly ?? false}
      onEdit={onEdit}
      fieldIssues={options.fieldIssues ?? {}}
      candidates={candidates}
    />
  );
  return { onEdit, ...view };
}

const field = (path: (string | number)[], value: unknown): DagEditOp => ({
  kind: 'setStepField',
  step: 'steps[0]',
  path,
  value,
});

function typeInto(element: HTMLElement, value: string) {
  fireEvent.change(element, { target: { value } });
  fireEvent.blur(element);
}

const error = (message: string): ValidationIssue => ({
  severity: ValidationIssueSeverity.error,
  source: ValidationIssueSource.build,
  message,
});

describe('Model section, one model', () => {
  it('offers the providers of the schema and writes the choice', () => {
    const { onEdit } = setup(
      ModelSection,
      chat({ provider: 'openai', model: 'gpt', prompt: 'hi' })
    );
    const select = screen.getByLabelText('Provider');
    expect(select).toHaveValue('openai');
    expect(within(select).getAllByRole('option').length).toBeGreaterThan(10);
    fireEvent.change(select, { target: { value: 'anthropic' } });
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'provider'], 'anthropic')
    );
  });

  it('removes the provider when the default is chosen', () => {
    const { onEdit } = setup(ModelSection, chat({ provider: 'openai' }));
    fireEvent.change(screen.getByLabelText('Provider'), {
      target: { value: '' },
    });
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'provider'], undefined));
  });

  it('writes the model on blur and removes the key when it is emptied', () => {
    const { onEdit } = setup(ModelSection, chat({ model: 'gpt' }));
    const model = screen.getByLabelText('Model');
    fireEvent.change(model, { target: { value: 'gpt-x' } });
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.blur(model);
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'model'], 'gpt-x'));
    typeInto(model, '');
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'model'], undefined)
    );
  });

  it('does not write an unchanged model', () => {
    const { onEdit } = setup(ModelSection, chat({ model: 'gpt' }));
    fireEvent.blur(screen.getByLabelText('Model'));
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('shows the base URL for a local provider and when it is set', () => {
    const local = setup(ModelSection, chat({ provider: 'local' }));
    expect(screen.getByLabelText('Base URL')).toBeInTheDocument();
    local.unmount();
    const cloud = setup(ModelSection, chat({ provider: 'openai' }));
    expect(screen.queryByLabelText('Base URL')).toBeNull();
    cloud.unmount();
    setup(
      ModelSection,
      chat({ provider: 'openai', base_url: 'https://proxy.test' })
    );
    expect(screen.getByLabelText('Base URL')).toHaveValue('https://proxy.test');
  });

  it('keeps the API key variable under Advanced', () => {
    const { onEdit } = setup(ModelSection, chat({ api_key_name: 'KEY' }));
    const advanced = screen.getByTestId('ve-model-advanced');
    expect(advanced).toHaveAttribute('open');
    const input = within(advanced).getByLabelText('API key variable');
    typeInto(input, '');
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'api_key_name'], undefined)
    );
  });

  it('takes a reference for the provider', () => {
    const { onEdit } = setup(ModelSection, chat({ model: 'm' }));
    fireEvent.change(screen.getByLabelText('Provider'), {
      target: { value: '\u0000reference' },
    });
    expect(onEdit).not.toHaveBeenCalled();
    typeInto(screen.getByLabelText('Provider'), '${params.PROVIDER}');
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'provider'], '${params.PROVIDER}')
    );
  });

  it('shows a provider that is not in the list as text, with a way back', () => {
    const { onEdit } = setup(
      ModelSection,
      chat({ provider: '${params.PROVIDER}' })
    );
    expect(screen.getByLabelText('Provider')).toHaveValue('${params.PROVIDER}');
    fireEvent.click(
      screen.getByRole('button', { name: 'Choose a provider from the list' })
    );
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'provider'], undefined));
  });

  it('shows the issues of its keys', () => {
    setup(ModelSection, chat({ model: 'm' }), {
      fieldIssues: { 'with.model': [error('unknown model')] },
    });
    expect(screen.getByText('unknown model')).toBeInTheDocument();
  });

  it('is locked when read-only', () => {
    setup(ModelSection, chat({ provider: 'openai', model: 'm' }), {
      readOnly: true,
    });
    expect(screen.getByLabelText('Provider')).toBeDisabled();
    expect(screen.getByLabelText('Model')).toBeDisabled();
  });
});

describe('Model section, fallback list', () => {
  const list = [
    { provider: 'openai', name: 'a', temperature: 0.2 },
    { provider: 'anthropic', name: 'b' },
  ];

  it('shows one row for each model and no single fields', () => {
    setup(ModelSection, chat({ model: list }));
    expect(screen.getAllByTestId('ve-model-row')).toHaveLength(2);
    expect(screen.getByLabelText('Name of model 2')).toHaveValue('b');
    expect(screen.getByLabelText('Provider of model 1')).toHaveValue('openai');
    expect(screen.queryByLabelText('Provider')).toBeNull();
    expect(screen.queryByLabelText('Model')).toBeNull();
  });

  it('edits one name or provider in place', () => {
    const { onEdit } = setup(ModelSection, chat({ model: list }));
    typeInto(screen.getByLabelText('Name of model 2'), 'c');
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'model', 1, 'name'], 'c')
    );
    fireEvent.change(screen.getByLabelText('Provider of model 1'), {
      target: { value: 'gemini' },
    });
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'model', 0, 'provider'], 'gemini')
    );
  });

  it('moves a row and keeps the other keys of the entries', () => {
    const { onEdit } = setup(ModelSection, chat({ model: list }));
    fireEvent.click(screen.getByRole('button', { name: 'Move a down' }));
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'model'], [list[1], list[0]])
    );
  });

  it('adds a row after the last one', () => {
    const { onEdit } = setup(ModelSection, chat({ model: list }));
    fireEvent.click(screen.getByRole('button', { name: 'Add a model' }));
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'model'], [...list, { provider: 'anthropic', name: '' }])
    );
  });

  it('removes a row, and the key when the last row goes', () => {
    const one = setup(ModelSection, chat({ model: list }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove model a' }));
    expect(one.onEdit).toHaveBeenCalledWith(
      field(['with', 'model'], [list[1]])
    );
    one.unmount();
    const last = setup(ModelSection, chat({ model: [list[0]] }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove model a' }));
    expect(last.onEdit).toHaveBeenCalledWith(
      field(['with', 'model'], undefined)
    );
  });

  it('has no row controls when read-only', () => {
    setup(ModelSection, chat({ model: list }), { readOnly: true });
    expect(screen.queryByRole('button', { name: /Move/ })).toBeNull();
    expect(screen.getByRole('button', { name: 'Add a model' })).toBeDisabled();
  });
});

describe('System message section', () => {
  it('is a tall box that the user can drag taller', () => {
    setup(SystemSection, chat({ system: 'You help.' }));
    const box = screen.getByLabelText('System message');
    expect(box.tagName).toBe('TEXTAREA');
    expect(box).toHaveAttribute('rows', '8');
    expect(box.className).toContain('resize-y');
    expect(box).toHaveValue('You help.');
  });

  it('writes on blur and removes the key when it is emptied', () => {
    const { onEdit } = setup(SystemSection, chat({ system: 'You help.' }));
    const box = screen.getByLabelText('System message');
    typeInto(box, 'Be brief.');
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'system'], 'Be brief.'));
    typeInto(box, '');
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'system'], undefined)
    );
  });

  it('writes the text that is not sent yet when the section goes away', () => {
    const { onEdit, unmount } = setup(SystemSection, chat({}));
    fireEvent.change(screen.getByLabelText('System message'), {
      target: { value: 'Draft' },
    });
    unmount();
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'system'], 'Draft'));
  });

  it('inserts a reference from the + button of the box', async () => {
    setup(SystemSection, chat({ system: 'Use ' }));
    fireEvent.click(screen.getByRole('button', { name: 'Insert reference' }));
    expect(
      await screen.findByRole('textbox', { name: 'Search references' })
    ).toBeInTheDocument();
  });

  it('shows a system value that is not text as read-only', () => {
    setup(SystemSection, chat({ system: { a: 1 } }));
    expect(screen.queryByLabelText('System message')).toBeNull();
    expect(screen.getByText(/Edit this field in YAML/)).toBeInTheDocument();
  });
});

describe('Prompt section, one prompt', () => {
  it('is a large box, marked required and empty for the new-step focus', () => {
    setup(PromptSection, chat({}));
    const box = screen.getByLabelText('Prompt');
    expect(box).toHaveAttribute('rows', '10');
    expect(box.className).toContain('resize-y');
    const holder = box.closest('[data-field-path]');
    expect(holder).toHaveAttribute('data-field-path', 'with.prompt');
    expect(holder).toHaveAttribute('data-required', 'true');
    expect(holder).toHaveAttribute('data-empty', 'true');
  });

  it('says what it is, in a helper line', () => {
    setup(PromptSection, chat({ prompt: 'hi' }));
    expect(
      screen.getByText(/The request that you send to the model/)
    ).toBeInTheDocument();
  });

  it('writes on blur and removes the key when it is emptied', () => {
    const { onEdit } = setup(PromptSection, chat({ prompt: 'hi' }));
    const box = screen.getByLabelText('Prompt');
    typeInto(box, 'line 1\nline 2');
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'prompt'], 'line 1\nline 2')
    );
    typeInto(box, '');
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'prompt'], undefined)
    );
  });

  it('shows the issue of `with` and of the prompt', () => {
    setup(PromptSection, chat({}), {
      fieldIssues: {
        with: [error('prompt or messages is required')],
        'with.prompt': [error('too short')],
      },
    });
    expect(
      screen.getByText('prompt or messages is required')
    ).toBeInTheDocument();
    expect(screen.getByText('too short')).toBeInTheDocument();
  });
});

describe('Prompt section, messages', () => {
  const messages = [
    { role: 'system', content: 'Rules' },
    { role: 'user', content: 'Question' },
  ];

  it('starts on the messages when the step has no prompt', () => {
    setup(PromptSection, chat({ messages }));
    expect(screen.getAllByTestId('ve-message-row')).toHaveLength(2);
    expect(screen.getByLabelText('Role of Message 1')).toHaveValue('system');
    expect(screen.getByLabelText('Text of Message 2')).toHaveValue('Question');
    expect(screen.queryByLabelText('Prompt')).toBeNull();
  });

  it('shows the prompt and a note when the step has both', () => {
    setup(PromptSection, chat({ prompt: 'hi', messages }));
    expect(screen.getByLabelText('Prompt')).toHaveValue('hi');
    expect(
      screen.getByText(/uses the prompt, not the messages/)
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Messages' }));
    expect(screen.getAllByTestId('ve-message-row')).toHaveLength(2);
  });

  it('shows no note when only one of them is set', () => {
    setup(PromptSection, chat({ messages }));
    expect(screen.queryByText(/uses the prompt, not the messages/)).toBeNull();
  });

  it('lets the user switch to the messages of a step that has a prompt', () => {
    const { onEdit } = setup(PromptSection, chat({ prompt: 'hi' }));
    fireEvent.click(screen.getByRole('button', { name: 'Messages' }));
    // Switching the form changes nothing in the YAML.
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Add a message' }));
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'messages'], [{ role: 'user', content: '' }])
    );
  });

  it('edits the role and the text of one row', () => {
    const { onEdit } = setup(PromptSection, chat({ messages }));
    fireEvent.change(screen.getByLabelText('Role of Message 2'), {
      target: { value: 'assistant' },
    });
    expect(onEdit).toHaveBeenCalledWith(
      field(['with', 'messages', 1, 'role'], 'assistant')
    );
    typeInto(screen.getByLabelText('Text of Message 1'), 'New rules\nline 2');
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'messages', 0, 'content'], 'New rules\nline 2')
    );
  });

  it('offers system, user and assistant as roles', () => {
    setup(PromptSection, chat({ messages }));
    const roles = within(screen.getByLabelText('Role of Message 1'))
      .getAllByRole('option')
      .map((option) => option.textContent);
    expect(roles).toEqual(['system', 'user', 'assistant']);
  });

  it('moves, adds and removes rows', () => {
    const { onEdit } = setup(PromptSection, chat({ messages }));
    fireEvent.click(
      screen.getByRole('button', { name: 'Move Message 1 down' })
    );
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'messages'], [messages[1], messages[0]])
    );
    fireEvent.click(screen.getByRole('button', { name: 'Add a message' }));
    expect(onEdit).toHaveBeenLastCalledWith(
      field(
        ['with', 'messages'],
        [...messages, { role: 'assistant', content: '' }]
      )
    );
    fireEvent.click(screen.getByRole('button', { name: 'Remove Message 2' }));
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'messages'], [messages[0]])
    );
  });

  it('removes the key when the last row goes', () => {
    const { onEdit } = setup(PromptSection, chat({ messages: [messages[0]] }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove Message 1' }));
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'messages'], undefined));
  });

  it('writes an edit that the YAML accepts, and drops the emptied key', () => {
    const text = [
      'steps:',
      '  - name: ask',
      '    action: chat.completion',
      '    with:',
      '      # keep this comment',
      '      model: m',
      '      messages:',
      '        - role: user',
      '          content: hi',
      '',
    ].join('\n');
    const { onEdit } = setup(
      PromptSection,
      chat({ model: 'm', messages: [{ role: 'user', content: 'hi' }] })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Remove Message 1' }));
    const op = onEdit.mock.calls[0]![0];
    const result = applyEdit(text, { ...op, step: 'steps[0]' } as DagEditOp);
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.text).toContain('# keep this comment');
      expect(result.text).not.toContain('messages');
    }
  });

  it('is locked when read-only', () => {
    setup(PromptSection, chat({ messages }), { readOnly: true });
    expect(screen.getByLabelText('Role of Message 1')).toBeDisabled();
    expect(screen.queryByRole('button', { name: /Move Message/ })).toBeNull();
    expect(
      screen.getByRole('button', { name: 'Add a message' })
    ).toBeDisabled();
  });
});

describe('Output section', () => {
  it('is off when the step has no output_schema', () => {
    setup(OutputSection, chat({ prompt: 'hi' }));
    expect(
      screen.getByRole('switch', { name: 'Require a specific output format' })
    ).toHaveAttribute('aria-checked', 'false');
    expect(screen.queryByTestId('monaco')).toBeNull();
  });

  it('adds a starting schema when it is switched on', () => {
    const { onEdit } = setup(OutputSection, chat({ prompt: 'hi' }));
    fireEvent.click(
      screen.getByRole('switch', { name: 'Require a specific output format' })
    );
    expect(onEdit).toHaveBeenCalledWith(
      field(['output_schema'], STARTER_SCHEMA)
    );
  });

  it('shows the schema as YAML and removes it when it is switched off', async () => {
    const { onEdit } = setup(
      OutputSection,
      chat(
        { prompt: 'hi' },
        {
          output_schema: {
            type: 'object',
            properties: { answer: { type: 'string' } },
          },
        }
      )
    );
    const editor = await screen.findByTestId('monaco');
    expect(editor).toHaveValue(
      'type: object\nproperties:\n  answer:\n    type: string'
    );
    fireEvent.click(
      screen.getByRole('switch', { name: 'Require a specific output format' })
    );
    expect(onEdit).toHaveBeenCalledWith(field(['output_schema'], undefined));
  });

  it('sends the parsed schema on blur', async () => {
    const { onEdit } = setup(
      OutputSection,
      chat({}, { output_schema: { type: 'object', properties: { a: {} } } })
    );
    const editor = await screen.findByTestId('monaco');
    typeInto(editor, 'type: object\nproperties:\n  city:\n    type: string');
    expect(onEdit).toHaveBeenCalledWith(
      field(['output_schema'], {
        type: 'object',
        properties: { city: { type: 'string' } },
      })
    );
  });

  it('shows a YAML error and sends nothing', async () => {
    const { onEdit } = setup(
      OutputSection,
      chat({}, { output_schema: { type: 'object' } })
    );
    const editor = await screen.findByTestId('monaco');
    typeInto(editor, 'type: [object');
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('refuses a text that is not a mapping', async () => {
    const { onEdit } = setup(
      OutputSection,
      chat({}, { output_schema: { type: 'object' } })
    );
    typeInto(await screen.findByTestId('monaco'), '- a\n- b');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'The output format must be a YAML mapping.'
    );
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('shows a schema that is not a map as read-only text', () => {
    setup(OutputSection, chat({}, { output_schema: 'schemas/out.json' }));
    expect(screen.queryByTestId('monaco')).toBeNull();
    expect(screen.getByText(/Edit this field in YAML/)).toBeInTheDocument();
  });

  it('shows the issues of output_schema', () => {
    setup(OutputSection, chat({}, { output_schema: { type: 'object' } }), {
      fieldIssues: { output_schema: [error('needs a property')] },
    });
    expect(screen.getByText('needs a property')).toBeInTheDocument();
  });
});

describe('Options section', () => {
  it('has stream on by default and writes `false` when it is switched off', () => {
    const { onEdit } = setup(OptionsSection, chat({ prompt: 'hi' }));
    const stream = screen.getByRole('switch', { name: 'Stream the answer' });
    expect(stream).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(stream);
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'stream'], false));
  });

  it('removes the key when stream is switched on again', () => {
    const { onEdit } = setup(OptionsSection, chat({ stream: false }));
    const stream = screen.getByRole('switch', { name: 'Stream the answer' });
    expect(stream).toHaveAttribute('aria-checked', 'false');
    fireEvent.click(stream);
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'stream'], undefined));
  });

  it('writes a number, and removes the key when the text is emptied', () => {
    const { onEdit } = setup(OptionsSection, chat({ temperature: 0.2 }));
    const temperature = screen.getByLabelText('Temperature');
    expect(temperature).toHaveValue('0.2');
    typeInto(temperature, '0.7');
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'temperature'], 0.7));
    typeInto(temperature, '');
    expect(onEdit).toHaveBeenLastCalledWith(
      field(['with', 'temperature'], undefined)
    );
  });

  it('shows an error for a number out of range and sends nothing', () => {
    const { onEdit } = setup(OptionsSection, chat({}));
    typeInto(screen.getByLabelText('Temperature'), '3');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Enter a number from 0 to 2'
    );
    typeInto(screen.getByLabelText('Top P'), 'x');
    expect(screen.getByText('Enter a number from 0 to 1')).toBeInTheDocument();
    typeInto(screen.getByLabelText('Maximum number of tokens'), '1.5');
    expect(
      screen.getAllByText('Enter a whole number, 1 or more').length
    ).toBeGreaterThan(0);
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('clears the error when a valid number follows', () => {
    const { onEdit } = setup(OptionsSection, chat({}));
    const temperature = screen.getByLabelText('Temperature');
    typeInto(temperature, '3');
    expect(screen.getByRole('alert')).toBeInTheDocument();
    typeInto(temperature, '1');
    expect(screen.queryByRole('alert')).toBeNull();
    expect(onEdit).toHaveBeenCalledWith(field(['with', 'temperature'], 1));
  });

  it('shows the default number of tool rounds', () => {
    setup(OptionsSection, chat({}));
    expect(
      screen.getByLabelText('Maximum number of tool rounds')
    ).toHaveAttribute('placeholder', 'Default: 10');
  });

  it('does not repeat the limits that the Tools section has', () => {
    setup(OptionsSection, chat({ max_tools: 5, tool_result_max_bytes: 10 }));
    expect(screen.queryByLabelText('Maximum number of tools')).toBeNull();
    expect(screen.queryByText(/tool result/)).toBeNull();
  });

  it('shows thinking and web_search only when they are set, read-only', () => {
    const none = setup(OptionsSection, chat({}));
    expect(screen.queryByText('thinking')).toBeNull();
    none.unmount();
    setup(
      OptionsSection,
      chat({ thinking: { enabled: true }, web_search: { enabled: true } })
    );
    expect(screen.getByText('thinking')).toBeInTheDocument();
    expect(screen.getByText('web_search')).toBeInTheDocument();
    expect(screen.getAllByText(/Edit this field in YAML/)).toHaveLength(2);
  });

  it('is locked when read-only', () => {
    setup(OptionsSection, chat({}), { readOnly: true });
    expect(screen.getByLabelText('Temperature')).toBeDisabled();
    expect(
      screen.getByRole('switch', { name: 'Stream the answer' })
    ).toBeDisabled();
  });
});
