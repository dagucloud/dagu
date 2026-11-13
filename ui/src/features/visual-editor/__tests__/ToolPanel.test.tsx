// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import type { ToolSelection, ValidationIssue } from '../contracts';
import { parseDagDoc } from '../doc';
import { ToolPanel } from '../ToolPanel';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));
vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

function serve(paramDefs: unknown[] = []) {
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
    (path: string) =>
      path === '/dags/{fileName}' ? { data: { dag: { paramDefs } } } : {}
  );
}

afterEach(() => vi.clearAllMocks());

const CHAT = `steps:
  - id: triage
    action: chat.completion
    with:
      prompt: hi
      dags:
        - name: refund
          params:
            customer: c1
            amount:
              from_ai:
                description: Euros
        - name: lookup
      tools:
        mcp:
          - name: docs
            url: https://mcp.example.com
            headers:
              Authorization: x
            allow: [search]
`;

const AGENT = `steps:
  - id: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
      tools:
        builtin:
          read: true
          shell:
            allow: [make test]
            deny: [git push *]
`;

const select = (
  category: ToolSelection['category'],
  index = 0
): ToolSelection => ({ step: 'steps[0]', category, index });

function issue(fieldPath: string, message: string): ValidationIssue {
  return {
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message,
    stepPath: 'steps[0]',
    fieldPath: `steps[0].${fieldPath}`,
  };
}

function setup(
  text: string,
  tool: ToolSelection,
  opts: {
    readOnly?: boolean;
    issues?: ValidationIssue[];
    paramDefs?: unknown[];
  } = {}
) {
  serve(opts.paramDefs);
  const onEdit = vi.fn();
  const onBack = vi.fn();
  const onClose = vi.fn();
  const model = parseDagDoc(text);
  const view = render(
    <ToolPanel
      model={model}
      step={model.steps[0]!}
      stepLabel="triage"
      tool={tool}
      issues={opts.issues ?? []}
      candidates={[]}
      readOnly={opts.readOnly ?? false}
      onEdit={onEdit}
      onBack={onBack}
      onClose={onClose}
    />
  );
  return { onEdit, onBack, onClose, model, ...view };
}

const op = (change: unknown) => ({
  kind: 'editStepTools',
  step: 'steps[0]',
  change,
});

const update = (
  category: string,
  index: number,
  path: (string | number)[],
  value: unknown
) => op({ kind: 'update', category, index, path, value });

function typeInto(label: string, value: string, index = 0) {
  const input = screen.getAllByLabelText(label)[index]!;
  fireEvent.change(input, { target: { value } });
  fireEvent.blur(input);
}

/** The titles of the sections, with their issue counts. */
function sectionTitles(): string[] {
  return screen
    .getAllByRole('button')
    .filter((button) => button.hasAttribute('aria-controls'))
    .map((button) => button.querySelector('span')?.textContent ?? '');
}

describe('the header of the tool panel', () => {
  it('has the icon of the kind, the name of the entry, the badge and the way back to the step', () => {
    const { onBack, onClose, container } = setup(CHAT, select('dags', 0));
    const panel = screen.getByTestId('ve-tool-panel');
    expect(panel).toHaveAttribute('data-kind', 'dag');
    expect(
      within(panel).getByRole('heading', { name: 'refund' })
    ).toBeInTheDocument();
    expect(
      container.querySelector('header [data-kind-icon="dag"]')
    ).not.toBeNull();
    expect(within(panel).getAllByTestId('ve-tool-kind')[0]).toHaveTextContent(
      'DAG'
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Back to step triage' })
    );
    expect(onBack).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('names an MCP server that has no name by its host, and a built-in entry by its kind', () => {
    const bare = CHAT.replace('name: docs', 'name: ""');
    setup(bare, select('mcp', 0));
    expect(
      screen.getByRole('heading', { name: 'mcp.example.com' })
    ).toBeInTheDocument();
  });

  it('counts the issues of the entry in the header', () => {
    setup(CHAT, select('dags', 0), {
      issues: [
        issue('with.dags[0]', 'unknown DAG'),
        issue('with.dags[0].params.customer', 'bad value'),
        issue('with.dags[1]', 'another entry'),
      ],
    });
    expect(
      within(screen.getByTestId('ve-tool-panel')).getAllByLabelText(
        '2 issues'
      )[0]
    ).toHaveTextContent('2');
    // An issue of the entry itself shows at the top; an issue of an input, by the input.
    expect(screen.getByText('unknown DAG')).toBeInTheDocument();
    expect(screen.getByText('bad value')).toBeInTheDocument();
    expect(screen.queryByText('another entry')).toBeNull();
  });
});

describe('the tool panel of a DAG tool', () => {
  it('has the sections General, Parameters and YAML, with the YAML closed', () => {
    setup(CHAT, select('dags', 0));
    expect(sectionTitles()).toEqual(['General', 'Parameters', 'YAML']);
    expect(screen.getByRole('button', { name: 'YAML' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
  });

  it('edits the name, and links to the page of the DAG', () => {
    const { onEdit } = setup(CHAT, select('dags', 0));
    typeInto('Name', 'refund-v2');
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 0, ['name'], 'refund-v2')
    );
    expect(
      screen.getByRole('link', { name: 'Open the workflow' })
    ).toHaveAttribute('href', '/dags/refund');
  });

  it('switches an input between a fixed value and the model, and removes it', () => {
    const { onEdit } = setup(CHAT, select('dags', 0));
    const rows = screen.getAllByTestId('ve-tool-param');
    expect(rows.map((row) => row.getAttribute('data-state'))).toEqual([
      'fixed',
      'from_ai',
    ]);
    typeInto('Value of customer', 'c2');
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 0, ['params', 'customer'], 'c2')
    );
    typeInto('Description of amount for the model', 'Amount to refund');
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 0, ['params', 'amount'], {
        from_ai: { description: 'Amount to refund' },
      })
    );
    fireEvent.click(
      within(rows[0]!).getByRole('button', { name: 'Model decides' })
    );
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 0, ['params', 'customer'], { from_ai: true })
    );
    fireEvent.click(
      within(rows[1]!).getByRole('button', { name: 'Fixed value' })
    );
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 0, ['params', 'amount'], '')
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove input customer' })
    );
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 0, ['params', 'customer'], undefined)
    );
  });

  it('keeps a number a number in a fixed value', () => {
    const text = CHAT.replace('customer: c1', 'customer: 5');
    const { onEdit } = setup(text, select('dags', 0));
    typeInto('Value of customer', '7');
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 0, ['params', 'customer'], 7)
    );
  });

  it('adds an input by name', () => {
    const { onEdit } = setup(CHAT, select('dags', 1));
    fireEvent.change(screen.getByLabelText('New input name'), {
      target: { value: 'channel' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add input' }));
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 1, ['params', 'channel'], { from_ai: true })
    );
  });

  it('lists the inputs that the DAG declares as inputs that the model decides', () => {
    setup(CHAT, select('dags', 1), {
      paramDefs: [
        { name: 'CUSTOMER', type: 'string', description: 'The customer' },
        { type: 'string' },
      ],
    });
    const row = screen.getByTestId('ve-tool-param');
    expect(row).toHaveAttribute('data-state', 'from_ai');
    expect(row).toHaveTextContent('CUSTOMER');
    expect(row).toHaveTextContent('The customer');
  });

  it('says that the model decides every input when there is none', () => {
    setup(CHAT, select('dags', 1));
    expect(
      screen.getByText('The model decides every input of this tool.')
    ).toBeInTheDocument();
  });

  it('opens an entry that still has an if key and changes nothing', () => {
    const text = CHAT.replace(
      '- name: lookup',
      '- name: lookup\n          if: [{condition: x, expected: y}]'
    );
    const { onEdit } = setup(text, select('dags', 1));
    expect(screen.queryByTestId('ve-tool-if')).toBeNull();
    expect(onEdit).not.toHaveBeenCalled();
  });
});

describe('the tool panel of an MCP server', () => {
  it('has the sections General, Connection, Allowed tools and YAML', () => {
    setup(CHAT, select('mcp', 0));
    expect(sectionTitles()).toEqual([
      'General',
      'Connection',
      'Allowed tools',
      'YAML',
    ]);
  });

  it('edits the name, the URL, the headers and the allow list', () => {
    const { onEdit } = setup(CHAT, select('mcp', 0));
    typeInto('Name', 'wiki');
    expect(onEdit).toHaveBeenLastCalledWith(update('mcp', 0, ['name'], 'wiki'));
    typeInto('Server URL', 'https://other.example.com');
    expect(onEdit).toHaveBeenLastCalledWith(
      update('mcp', 0, ['url'], 'https://other.example.com')
    );
    // The headers use the key and value rows of the parameters form.
    const headers = screen.getByRole('group', { name: 'Headers' });
    const value = within(headers).getByDisplayValue('x');
    fireEvent.change(value, { target: { value: 'y' } });
    fireEvent.blur(value);
    expect(onEdit).toHaveBeenLastCalledWith(
      update('mcp', 0, ['headers'], { Authorization: 'y' })
    );
    fireEvent.click(within(headers).getByRole('button', { name: 'Remove' }));
    expect(onEdit).toHaveBeenLastCalledWith(
      update('mcp', 0, ['headers'], undefined)
    );
    fireEvent.change(screen.getByLabelText('New item'), {
      target: { value: 'fetch' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add item' }));
    expect(onEdit).toHaveBeenLastCalledWith(
      update('mcp', 0, ['allow'], ['search', 'fetch'])
    );
    fireEvent.click(screen.getByRole('button', { name: 'Remove search' }));
    expect(onEdit).toHaveBeenLastCalledWith(update('mcp', 0, ['allow'], []));
  });

  it('counts the issues of each section', () => {
    setup(CHAT, select('mcp', 0), {
      issues: [
        issue('with.tools.mcp[0].url', 'url is required'),
        issue('with.tools.mcp[0].allow', 'allow is required'),
        issue('with.tools.mcp[0].name', 'bad name'),
      ],
    });
    const count = (title: string) =>
      screen
        .getByRole('button', { name: new RegExp(`^${title}`) })
        .querySelector('span:last-child')?.textContent;
    expect(count('Connection')).toBe('1');
    expect(count('Allowed tools')).toBe('1');
    expect(count('General')).toBe('1');
    expect(screen.getByText('url is required')).toBeInTheDocument();
  });
});

// The panel works for the DAG and MCP tools of an agent step as for a chat step.
// The step is the step `code` of examples/agent-worker-demo/dags/agent-dev-real.yaml.
const AGENT_BRIDGE = `steps:
  - id: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
      max_tool_calls: 6
      dags:
        - name: lookup-ticket
          params:
            CUSTOMER_ID: c-42
            QUESTION:
              from_ai:
                description: The question
      tools:
        mcp:
          - name: dagu
            url: http://dagu-coordinator:8080/mcp
            allow: [dagu_read]
        builtin:
          read: true
`;

describe('the tool panel of an agent step', () => {
  it('edits a DAG tool as on a chat step', () => {
    const { onEdit } = setup(AGENT_BRIDGE, select('dags', 0));
    expect(screen.getByTestId('ve-tool-panel')).toHaveAttribute(
      'data-kind',
      'dag'
    );
    expect(sectionTitles()).toEqual(['General', 'Parameters', 'YAML']);
    expect(
      screen
        .getAllByTestId('ve-tool-param')
        .map((row) => row.getAttribute('data-state'))
    ).toEqual(['fixed', 'from_ai']);
    typeInto('Value of CUSTOMER_ID', 'c-7');
    expect(onEdit).toHaveBeenLastCalledWith(
      update('dags', 0, ['params', 'CUSTOMER_ID'], 'c-7')
    );
  });

  it('edits the allowed tools of an MCP server', () => {
    const { onEdit } = setup(AGENT_BRIDGE, select('mcp', 0));
    expect(screen.getByTestId('ve-tool-panel')).toHaveAttribute(
      'data-kind',
      'mcp'
    );
    expect(sectionTitles()).toEqual([
      'General',
      'Connection',
      'Allowed tools',
      'YAML',
    ]);
    typeInto('Name', 'dagu-mcp');
    expect(onEdit).toHaveBeenLastCalledWith(
      update('mcp', 0, ['name'], 'dagu-mcp')
    );
  });

  it('keeps the built-in tools next to the other tools', () => {
    setup(AGENT_BRIDGE, select('builtin'));
    expect(screen.getByTestId('ve-tool-panel')).toHaveAttribute(
      'data-kind',
      'builtin'
    );
  });
});

describe('the tool panel of the built-in tools', () => {
  it('has one section with the switches and the shell patterns', () => {
    setup(AGENT, select('builtin'));
    expect(screen.getByTestId('ve-tool-panel')).toHaveAttribute(
      'data-kind',
      'builtin'
    );
    expect(
      screen.getByRole('heading', { name: 'Built-in tools' })
    ).toBeInTheDocument();
    expect(sectionTitles()).toEqual(['Built-in tools', 'YAML']);
    const block = screen.getByTestId('ve-builtin-tools');
    expect(within(block).getByLabelText('Read files')).toBeChecked();
    expect(within(block).getByLabelText('Write files')).not.toBeChecked();
    expect(within(block).getByText('make test')).toBeInTheDocument();
    expect(within(block).getByText('git push *')).toBeInTheDocument();
  });

  it('writes a switch as true and removes the key when it goes off', () => {
    const { onEdit } = setup(AGENT, select('builtin'));
    fireEvent.click(screen.getByLabelText('Write files'));
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['with', 'tools', 'builtin', 'write'],
      value: true,
    });
    fireEvent.click(screen.getByLabelText('Read files'));
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['with', 'tools', 'builtin', 'read'],
      value: undefined,
    });
  });

  it('removes the built-in tools and closes', () => {
    const { onEdit, onClose } = setup(AGENT, select('builtin'));
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove tool Built-in tools' })
    );
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['with', 'tools', 'builtin'],
      value: undefined,
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('shows the YAML of the built-in tools, and applies a change as one field', async () => {
    const { onEdit } = setup(AGENT, select('builtin'));
    fireEvent.click(screen.getByRole('button', { name: 'YAML' }));
    const box = await screen.findByRole('textbox', { name: 'Tool YAML' });
    expect((box as HTMLTextAreaElement).value).toBe(
      'read: true\nshell:\n  allow: [make test]\n  deny: [git push *]'
    );
    fireEvent.change(box, { target: { value: 'read: true\nsearch: true' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['with', 'tools', 'builtin'],
      value: { read: true, search: true },
    });
  });
});

describe('removing a tool from the panel', () => {
  it('sends the remove operation of the entry and closes the panel', () => {
    const { onEdit, onClose } = setup(CHAT, select('dags', 0));
    fireEvent.click(screen.getByRole('button', { name: 'Remove tool refund' }));
    expect(onEdit).toHaveBeenCalledWith(
      op({ kind: 'remove', category: 'dags', index: 0 })
    );
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('the YAML section of the tool panel', () => {
  it('shows the entry as the file writes it, and applies a new text as one replace', async () => {
    const { onEdit } = setup(CHAT, select('dags', 1));
    fireEvent.click(screen.getByRole('button', { name: 'YAML' }));
    const box = (await screen.findByRole('textbox', {
      name: 'Tool YAML',
    })) as HTMLTextAreaElement;
    expect(box.value).toBe('name: lookup');
    const apply = screen.getByRole('button', { name: 'Apply' });
    expect(apply).toBeDisabled();
    fireEvent.change(box, { target: { value: 'name: lookup-v2' } });
    fireEvent.click(apply);
    expect(onEdit).toHaveBeenLastCalledWith(
      op({
        kind: 'replace',
        category: 'dags',
        index: 1,
        yaml: 'name: lookup-v2',
      })
    );
  });

  it('shows the error of a text that does not parse, and sends nothing', async () => {
    const { onEdit } = setup(CHAT, select('dags', 1));
    fireEvent.click(screen.getByRole('button', { name: 'YAML' }));
    fireEvent.change(
      await screen.findByRole('textbox', { name: 'Tool YAML' }),
      {
        target: { value: '- a\n- b' },
      }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(screen.getByRole('alert')).toHaveTextContent(
      'The tool entry must be a YAML mapping.'
    );
    expect(onEdit).not.toHaveBeenCalled();
  });
});

describe('the tool panel in a view that only reads', () => {
  it('shows the fields disabled and has no delete, add or apply control', () => {
    setup(CHAT, select('dags', 0), { readOnly: true });
    expect(screen.getAllByLabelText('Name')[0]).toBeDisabled();
    expect(screen.getByLabelText('Value of customer')).toBeDisabled();
    expect(screen.queryByRole('button', { name: /Remove tool/ })).toBeNull();
    expect(screen.queryByLabelText('New input name')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'YAML' }));
    expect(screen.queryByRole('button', { name: 'Apply' })).toBeNull();
    // The way back and the close button still work.
    expect(
      screen.getByRole('button', { name: 'Back to step triage' })
    ).toBeEnabled();
  });

  it('takes no focus', () => {
    setup(CHAT, select('dags', 0), { readOnly: true });
    expect(
      screen.getByTestId('ve-tool-panel').contains(document.activeElement)
    ).toBe(false);
  });
});

describe('opening the tool panel', () => {
  it('puts the focus on the first field', () => {
    setup(CHAT, select('mcp', 0));
    expect(document.activeElement).toBe(screen.getAllByLabelText('Name')[0]);
  });

  it('puts the focus on the first switch of the built-in tools', () => {
    setup(AGENT, select('builtin'));
    expect(document.activeElement).toBe(screen.getByLabelText('Read files'));
  });
});

describe('a tool that cannot be shown', () => {
  it('says that the tool no longer exists, with a close button', () => {
    const { onClose } = setup(CHAT, select('dags', 9));
    expect(screen.getByText('This tool no longer exists.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalled();
  });

  it('shows the reason for an item that is not a mapping, and the YAML of the item', () => {
    const text = CHAT.replace('- name: lookup', '- lonely');
    setup(text, select('dags', 1));
    expect(
      screen.getByText('A tool entry is a mapping with a name.')
    ).toBeInTheDocument();
    expect(sectionTitles()).toEqual(['YAML']);
  });

  it('is read-only while the YAML has errors, and says so', () => {
    const model = parseDagDoc(CHAT);
    render(
      <ToolPanel
        model={{ ...model, ok: false }}
        step={model.steps[0]!}
        stepLabel="triage"
        tool={select('dags', 0)}
        issues={[]}
        candidates={[]}
        readOnly={false}
        onEdit={vi.fn()}
        onBack={vi.fn()}
        onClose={vi.fn()}
      />
    );
    expect(
      screen.getByText(/Fix the YAML errors to edit this step/)
    ).toBeInTheDocument();
    expect(screen.getAllByLabelText('Name')[0]).toBeDisabled();
  });
});
