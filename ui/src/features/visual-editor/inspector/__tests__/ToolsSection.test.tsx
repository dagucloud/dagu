// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { StepView } from '../../contracts';
import { applyEdit, parseDagDoc } from '../../doc';
import { applyTextEdits } from '../../doc/patch';
import { ToolsSection } from '../sections/tools/ToolsSection';
import { makeStep } from '../../canvas/__tests__/fixtures';

afterEach(() => vi.clearAllMocks());

/**
 * A chat step. `tools` is the list form, or a map with the lists `dags` and
 * `mcp`: the DAG tools go to `with.dags`, the servers to `with.tools.mcp`.
 */
function chat(tools: unknown, extra: object = {}): StepView {
  const map =
    tools && !Array.isArray(tools) ? (tools as Record<string, unknown>) : null;
  const { dags: dagList, ...others } = map ?? {};
  return makeStep({
    key: 'steps[0]',
    kind: 'action',
    action: 'chat.completion',
    value: {
      action: 'chat.completion',
      with: {
        prompt: 'hi',
        ...(dagList ? { dags: dagList } : {}),
        ...(map
          ? Object.keys(others).length > 0
            ? { tools: others }
            : {}
          : tools
            ? { tools }
            : {}),
        ...extra,
      },
    },
  });
}

/** The tools of a step with these DAG names. */
function dags(...names: string[]) {
  return { dags: names.map((name) => ({ name })) };
}

function setup(
  step: StepView,
  opts: {
    readOnly?: boolean;
    fieldIssues?: Parameters<typeof ToolsSection>[0]['fieldIssues'];
    withHost?: boolean;
  } = {}
) {
  const onEdit = vi.fn();
  const onOpenTool = vi.fn();
  const onAddTool = vi.fn();
  const withHost = opts.withHost ?? true;
  const view = render(
    <ToolsSection
      step={step}
      candidates={[]}
      readOnly={opts.readOnly ?? false}
      onEdit={onEdit}
      fieldIssues={opts.fieldIssues ?? {}}
      {...(withHost ? { onOpenTool, onAddTool } : {})}
    />
  );
  return { onEdit, onOpenTool, onAddTool, ...view };
}

const op = (change: unknown) => ({
  kind: 'editStepTools',
  step: 'steps[0]',
  change,
});

describe('ToolsSection list', () => {
  it('says that there is no tool yet', () => {
    setup(chat(undefined));
    expect(screen.getByText('No tools yet')).toBeInTheDocument();
    expect(screen.queryAllByTestId('ve-tool-entry')).toHaveLength(0);
  });

  it('lists the entries by category, in order, with the icon and the badge of their kind', () => {
    setup(
      chat({
        mcp: [{ name: 'docs', url: 'https://mcp.example.com', allow: ['x'] }],
        dags: [{ name: 'lookup' }, { name: 'refund' }],
      })
    );
    const rows = screen.getAllByTestId('ve-tool-entry');
    expect(rows.map((row) => row.getAttribute('data-kind'))).toEqual([
      'dag',
      'dag',
      'mcp',
    ]);
    expect(rows.map((row) => row.getAttribute('data-field-path'))).toEqual([
      'with.dags[0]',
      'with.dags[1]',
      'with.tools.mcp[0]',
    ]);
    expect(rows[0]).toHaveTextContent('lookup');
    expect(rows[2]).toHaveTextContent('docs');
    expect(
      rows.map((row) => within(row).getByTestId('ve-tool-kind').textContent)
    ).toEqual(['DAG', 'DAG', 'MCP']);
    expect(
      rows.map((row) =>
        row.querySelector('[data-kind-icon]')?.getAttribute('data-kind-icon')
      )
    ).toEqual(['dag', 'dag', 'mcp']);
  });

  it('has no inline editor: a row is a name, a badge and a delete button', () => {
    setup(chat({ dags: [{ name: 'refund', params: { amount: 5 } }] }));
    expect(screen.queryByTestId('ve-tool-editor')).toBeNull();
    expect(screen.queryByTestId('ve-tool-params')).toBeNull();
    expect(screen.queryByRole('button', { name: /Move/ })).toBeNull();
  });

  it('selects the entry from a click on its row, and edits nothing', () => {
    const { onOpenTool, onEdit } = setup(
      chat({
        ...dags('a', 'b'),
        mcp: [{ name: 'c', url: 'https://mcp.example.com', allow: ['x'] }],
      })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Edit tool b' }));
    expect(onOpenTool).toHaveBeenLastCalledWith({
      step: 'steps[0]',
      category: 'dags',
      index: 1,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Edit tool c' }));
    expect(onOpenTool).toHaveBeenLastCalledWith({
      step: 'steps[0]',
      category: 'mcp',
      index: 0,
    });
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('removes an entry from its delete button', () => {
    const { onEdit } = setup(
      chat({
        ...dags('a', 'b'),
        mcp: [{ name: 'c', url: 'https://mcp.example.com', allow: ['x'] }],
      })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Remove tool c' }));
    expect(onEdit).toHaveBeenLastCalledWith(
      op({ kind: 'remove', category: 'mcp', index: 0 })
    );
  });

  it('counts the issues of an entry in its row, and lists them in the panel only', () => {
    setup(chat(dags('a')), {
      fieldIssues: {
        'with.dags[0]': [
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'unknown DAG',
          },
        ],
        'with.dags[0].params.x': [
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'bad input',
          },
        ],
      },
    });
    expect(screen.getByLabelText('2 issues')).toHaveTextContent('2');
    expect(screen.queryByText('unknown DAG')).toBeNull();
  });

  it('has no delete button in a read-only step, and a disabled add button', () => {
    setup(chat(dags('a', 'b')), { readOnly: true });
    expect(screen.queryByRole('button', { name: /Remove tool/ })).toBeNull();
    expect(screen.getByRole('button', { name: 'Add a tool' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Add a DAG' })).toBeDisabled();
    // The rows still select the tool: its panel shows it read-only.
    expect(
      screen.getByRole('button', { name: 'Edit tool a' })
    ).not.toBeDisabled();
  });

  it('shows the issues of the tool map and of the list of DAGs under the list', () => {
    const issue = (message: string) => [
      {
        severity: ValidationIssueSeverity.error,
        source: ValidationIssueSource.build,
        message,
      },
    ];
    setup(chat(dags('a')), {
      fieldIssues: {
        'with.tools': issue('too many tools'),
        'with.dags': issue('not a list'),
      },
    });
    expect(screen.getByText('too many tools')).toBeInTheDocument();
    expect(screen.getByText('not a list')).toBeInTheDocument();
  });
});

describe('ToolsSection add buttons', () => {
  it('opens the dock that picks a tool, and has no picker of its own', () => {
    const { onAddTool } = setup(chat(undefined));
    fireEvent.click(screen.getByRole('button', { name: 'Add a tool' }));
    expect(onAddTool).toHaveBeenCalledTimes(1);
    expect(onAddTool).toHaveBeenLastCalledWith('tools');
    expect(screen.queryByTestId('ve-tool-picker')).toBeNull();
  });

  // The DAG tools have their own dock, as they have their own key and card.
  it('opens the dock that picks a DAG from the button of the DAGs', () => {
    const { onAddTool } = setup(chat(undefined));
    fireEvent.click(screen.getByRole('button', { name: 'Add a DAG' }));
    expect(onAddTool).toHaveBeenCalledTimes(1);
    expect(onAddTool).toHaveBeenLastCalledWith('dags');
  });

  it('has no add button and a disabled row without a host that opens a panel', () => {
    setup(chat(dags('a')), { withHost: false });
    expect(screen.queryByRole('button', { name: 'Add a tool' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Add a DAG' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Edit tool a' })).toBeDisabled();
  });
});

describe('ToolsSection limits', () => {
  it('keeps the limits under an Advanced disclosure and sets them', () => {
    const { onEdit } = setup(chat(dags('a'), { max_tools: 8 }));
    const advanced = screen.getByTestId('ve-tool-advanced');
    expect(advanced.tagName).toBe('DETAILS');
    expect(advanced).not.toHaveAttribute('open');
    const max = screen.getByLabelText('Maximum number of tools');
    expect(max).toHaveValue('8');
    fireEvent.change(max, { target: { value: '12' } });
    fireEvent.blur(max);
    expect(onEdit).toHaveBeenLastCalledWith(
      op({ kind: 'setLimit', key: 'max_tools', value: 12 })
    );
    const bytes = screen.getByLabelText(
      'Maximum size of a tool result, in bytes (0: no limit)'
    );
    expect(bytes).toHaveAttribute('placeholder', 'Default: 524288');
    fireEvent.change(bytes, { target: { value: '0' } });
    fireEvent.blur(bytes);
    expect(onEdit).toHaveBeenLastCalledWith(
      op({ kind: 'setLimit', key: 'tool_result_max_bytes', value: 0 })
    );
  });

  it('removes a limit with an empty box and refuses a bad number', () => {
    const { onEdit } = setup(chat(dags('a'), { max_tools: 8 }));
    const max = screen.getByLabelText('Maximum number of tools');
    fireEvent.change(max, { target: { value: '' } });
    fireEvent.blur(max);
    expect(onEdit).toHaveBeenLastCalledWith(
      op({ kind: 'setLimit', key: 'max_tools', value: undefined })
    );
    onEdit.mockClear();
    fireEvent.change(max, { target: { value: 'many' } });
    fireEvent.blur(max);
    expect(onEdit).not.toHaveBeenCalled();
    expect(screen.getByText('Enter a whole number')).toBeInTheDocument();
  });
});

// The list form `tools: [name]` is the released one; it reads as `dags` entries.
describe('ToolsSection list form', () => {
  const NOTE = 'The list form of tools is deprecated. Use the dags key.';

  it('lists the names as DAG entries, with the note', () => {
    const { onEdit, onOpenTool } = setup(chat(['lookup', 'refund']));
    const rows = screen.getAllByTestId('ve-tool-entry');
    expect(rows.map((row) => row.getAttribute('data-kind'))).toEqual([
      'dag',
      'dag',
    ]);
    expect(rows[0]).toHaveTextContent('lookup');
    expect(rows[1]).toHaveTextContent('refund');
    expect(rows.map((row) => row.getAttribute('data-field-path'))).toEqual([
      'with.tools[0]',
      'with.tools[1]',
    ]);
    expect(screen.getByTestId('ve-tools-list-form-note')).toHaveTextContent(
      NOTE
    );
    expect(screen.queryByText('No tools yet')).toBeNull();
    // A name selects the entry of `dags` that the first edit writes.
    fireEvent.click(screen.getByRole('button', { name: 'Edit tool refund' }));
    expect(onOpenTool).toHaveBeenLastCalledWith({
      step: 'steps[0]',
      category: 'dags',
      index: 1,
    });
    // Reading changes nothing.
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('shows no note for the key dags or for no tools', () => {
    setup(chat(dags('lookup')));
    expect(screen.queryByText(NOTE)).toBeNull();
    expect(screen.getAllByTestId('ve-tool-entry')).toHaveLength(1);
  });

  it('shows no note for a step without tools', () => {
    setup(chat(undefined));
    expect(screen.queryByText(NOTE)).toBeNull();
  });

  it('sends the same remove operation as the key dags', () => {
    const { onEdit } = setup(chat(['a', 'b']));
    fireEvent.click(screen.getByRole('button', { name: 'Remove tool b' }));
    expect(onEdit).toHaveBeenLastCalledWith(
      op({ kind: 'remove', category: 'dags', index: 1 })
    );
  });

  // The section reads the YAML and sends operations; the edit of the YAML rewrites the key.
  describe('with the YAML as the source', () => {
    const LIST = `steps:
  - id: triage
    action: chat.completion
    with:
      prompt: hi
      tools: [lookup, refund]
`;

    function Harness({
      initial,
      seen,
    }: {
      initial: string;
      seen: { text: string };
    }) {
      const [text, setText] = useState(initial);
      seen.text = text;
      const value = parseDagDoc(text).steps[0]!.value as Record<
        string,
        unknown
      >;
      const step = makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value,
      });
      return (
        <ToolsSection
          step={step}
          candidates={[]}
          readOnly={false}
          onEdit={(editOp) => {
            const result = applyEdit(text, editOp);
            if (result.ok) setText(applyTextEdits(text, result.edits));
          }}
          fieldIssues={{}}
        />
      );
    }

    it('keeps the text and the note until the first edit', () => {
      const seen = { text: '' };
      render(<Harness initial={LIST} seen={seen} />);
      expect(screen.getByTestId('ve-tools-list-form-note')).toBeInTheDocument();
      expect(seen.text).toBe(LIST);
    });

    it('rewrites the list to the key dags on a remove, and drops the note', () => {
      const seen = { text: '' };
      render(<Harness initial={LIST} seen={seen} />);
      fireEvent.click(
        screen.getByRole('button', { name: 'Remove tool lookup' })
      );
      expect(seen.text).toBe(`steps:
  - id: triage
    action: chat.completion
    with:
      prompt: hi
      dags:
        - name: refund
`);
      expect(screen.queryByTestId('ve-tools-list-form-note')).toBeNull();
      expect(screen.getAllByTestId('ve-tool-entry')).toHaveLength(1);
      expect(screen.getByTestId('ve-tool-entry')).toHaveAttribute(
        'data-field-path',
        'with.dags[0]'
      );
    });
  });
});
