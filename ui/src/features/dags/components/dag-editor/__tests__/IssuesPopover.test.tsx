// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  type components,
  ValidationIssueSeverity,
  ValidationIssueSource,
  ValueReferenceNoticeClass,
  ValueReferenceNoticeReason,
} from '@/api/v1/schema';
import { IssuesPopover } from '../IssuesPopover';

type ValidationIssue = components['schemas']['ValidationIssue'];
type ValueReferenceNotice = components['schemas']['ValueReferenceNotice'];

const unknownStep: ValueReferenceNotice = {
  message: 'Step id fetch does not exist',
  fieldPath: 'steps[1].run',
  token: '${steps.fetch.stdout}',
  reason: ValueReferenceNoticeReason.unknown_step_id,
  class: ValueReferenceNoticeClass.defect,
};

const issues: ValidationIssue[] = [
  {
    severity: ValidationIssueSeverity.warning,
    source: ValidationIssueSource.warning,
    message: 'Step load has no explicit working_dir',
    stepName: 'load',
  },
  {
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message: 'invalid keys: nosuchfield',
    stepName: 'extract',
  },
  {
    severity: ValidationIssueSeverity.notice,
    source: ValidationIssueSource.valueReference,
    message: 'another text',
    stepName: 'report',
    fieldPath: 'steps[1].run',
  },
];

function openPopover() {
  fireEvent.click(screen.getByRole('button', { name: /^Issues \(\d+\)$/ }));
  return screen.getByRole('dialog', { name: 'Issues' });
}

// parity:spec-warnings-errors-banner
// parity:dup-16
describe('IssuesPopover', () => {
  it('shows no button without issues', () => {
    render(<IssuesPopover warnings={[]} errors={[]} />);
    expect(
      screen.queryByRole('button', { name: /Issues/ })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('');
  });

  // The triangle with the count is the only trigger, and it shows with errors.
  it('shows no button for warnings only, and no Issues pill', () => {
    render(
      <IssuesPopover
        warnings={['Step load has no explicit working_dir']}
        errors={[]}
        valueReferences={[unknownStep]}
      />
    );
    expect(
      screen.queryByRole('button', { name: /Issues/ })
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/Issues \(/)).toBeNull();
  });

  it('shows the triangle and the count of every issue once there is an error', () => {
    render(
      <IssuesPopover
        warnings={['Step load has no explicit working_dir']}
        errors={['other error']}
        valueReferences={[unknownStep]}
      />
    );
    const button = screen.getByRole('button', { name: 'Issues (3)' });
    expect(button).toHaveTextContent(/^3$/);
    expect(button.querySelector('svg')).not.toBeNull();
  });

  it('lists the warnings and the errors in their groups', () => {
    render(
      <IssuesPopover
        warnings={['Step load has no explicit working_dir']}
        errors={['[3:1] invalid keys: nosuchfield', 'other error']}
      />
    );

    const button = screen.getByRole('button', { name: 'Issues (3)' });
    expect(button).toHaveAttribute('aria-expanded', 'false');
    const panel = openPopover();
    expect(button).toHaveAttribute('aria-expanded', 'true');

    const warnings = within(panel).getByRole('region', {
      name: 'Warnings (1)',
    });
    expect(warnings).toHaveTextContent('working_dir');
    const errors = within(panel).getByRole('region', { name: 'Errors (2)' });
    expect(errors).toHaveTextContent('nosuchfield');
    expect(errors).toHaveTextContent('other error');
    expect(
      within(panel).queryByRole('region', { name: /Value references/ })
    ).not.toBeInTheDocument();
  });

  it('reveals the step of a row and closes', () => {
    const onRevealStep = vi.fn();
    render(
      <IssuesPopover
        warnings={['Step load has no explicit working_dir']}
        // The position prefix of an error string is not in the issue.
        errors={['[3:1] invalid keys: nosuchfield', 'other error']}
        valueReferences={[unknownStep]}
        issues={issues}
        onRevealStep={onRevealStep}
      />
    );

    let panel = openPopover();
    fireEvent.click(within(panel).getByRole('button', { name: /working_dir/ }));
    expect(onRevealStep).toHaveBeenLastCalledWith('load');
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();

    panel = openPopover();
    fireEvent.click(within(panel).getByRole('button', { name: /nosuchfield/ }));
    expect(onRevealStep).toHaveBeenLastCalledWith('extract');

    // A value reference finds its step by its field path.
    panel = openPopover();
    fireEvent.click(
      within(panel).getByRole('button', { name: /fetch does not exist/ })
    );
    expect(onRevealStep).toHaveBeenLastCalledWith('report');

    // A message without a step is text, not a button.
    panel = openPopover();
    expect(
      within(panel).queryByRole('button', { name: /other error/ })
    ).not.toBeInTheDocument();
    expect(within(panel).getByText('other error')).toBeInTheDocument();
  });

  // parity:spec-value-reference-notices
  it('keeps the value-reference details dialog for its group', () => {
    render(
      <IssuesPopover
        warnings={[]}
        errors={['an error']}
        valueReferences={[unknownStep]}
      />
    );

    const panel = openPopover();
    const group = within(panel).getByRole('region', {
      name: 'Value references (1)',
    });
    expect(group).toHaveTextContent('Step id fetch does not exist');
    fireEvent.click(within(group).getByRole('button', { name: /Details/ }));

    // The popover hides; the dialog of the notices stays open over the page.
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();
    const dialog = screen.getByRole('dialog', {
      name: 'Value Reference Notices',
    });
    expect(dialog).toHaveTextContent('Needs a fix');
    expect(dialog).toHaveTextContent('${steps.fetch.stdout}');
  });

  it('announces the changes of the count politely', () => {
    const view = render(<IssuesPopover warnings={[]} errors={['a', 'b']} />);
    const status = screen.getByRole('status');
    expect(status).toHaveAttribute('aria-live', 'polite');
    expect(status).toHaveTextContent('2 issues');

    view.rerender(<IssuesPopover warnings={[]} errors={['a']} />);
    expect(status).toHaveTextContent('1 issue');

    view.rerender(<IssuesPopover warnings={[]} errors={[]} />);
    expect(status).toHaveTextContent('No issues');
  });

  it('closes on Escape and on a click outside', () => {
    render(<IssuesPopover warnings={[]} errors={['a']} />);
    const button = screen.getByRole('button', { name: 'Issues (1)' });

    openPopover();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();
    expect(button).toHaveFocus();

    const panel = openPopover();
    fireEvent.pointerDown(panel);
    expect(screen.getByRole('dialog', { name: 'Issues' })).toBeVisible();
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();
  });

  // An error that no step owns says so, instead of looking like a step error.
  it('marks an error without a step as workflow-level', () => {
    render(
      <IssuesPopover
        warnings={[]}
        errors={['invalid keys: nosuchfield', 'step command is empty']}
        issues={issues}
        onRevealStep={vi.fn()}
      />
    );

    const panel = openPopover();
    const errors = within(panel).getByRole('region', { name: 'Errors (2)' });
    expect(within(errors).getAllByText('Workflow-level')).toHaveLength(1);
  });
});

describe('IssuesPopover with the issues of the client', () => {
  const found: ValidationIssue[] = [
    {
      severity: ValidationIssueSeverity.error,
      source: ValidationIssueSource.build,
      message: 'Depends on the unknown step "ghost".',
      stepName: 'late',
      stepPath: 'steps[1]',
      fieldPath: 'steps[1].depends',
      line: 12,
    },
    {
      severity: ValidationIssueSeverity.error,
      source: ValidationIssueSource.build,
      message: 'Action is empty.',
      stepName: 'early',
      stepPath: 'steps[0]',
      fieldPath: 'steps[0].action',
      line: 4,
    },
    {
      severity: ValidationIssueSeverity.warning,
      source: ValidationIssueSource.warning,
      message: 'The step has no id, as the other steps have.',
      stepName: 'late',
      stepPath: 'steps[1]',
      line: 10,
    },
  ];
  const lineOf = (issue: ValidationIssue) => issue.line ?? null;

  it('lists an issue that no message says, sorted by line, with its line', () => {
    render(
      <IssuesPopover
        warnings={[]}
        errors={['A message of the server']}
        issues={found}
        lineOf={lineOf}
      />
    );
    expect(screen.getByRole('button', { name: 'Issues (4)' })).toBeVisible();
    const panel = openPopover();
    const errors = within(
      within(panel).getByRole('region', { name: 'Errors (3)' })
    ).getAllByRole('listitem');
    // The message of the server has no line: it is about the whole file.
    expect(errors[0]).toHaveTextContent('A message of the server');
    expect(errors[1]).toHaveTextContent('Action is empty.');
    expect(errors[1]).toHaveTextContent('early');
    expect(errors[1]).toHaveTextContent('line 4');
    expect(errors[2]).toHaveTextContent('line 12');
    expect(
      within(panel).getByRole('region', { name: 'Warnings (1)' })
    ).toHaveTextContent('line 10');
  });

  it('says an issue of the server and of the client once', () => {
    render(
      <IssuesPopover
        warnings={[]}
        errors={['Action is empty.']}
        issues={found}
        lineOf={lineOf}
      />
    );
    // The error of the server, the other error and the warning.
    expect(screen.getByRole('button', { name: 'Issues (3)' })).toBeVisible();
  });

  it('gives the issue of a row to the host, with the line', () => {
    const onRevealIssue = vi.fn();
    const onRevealStep = vi.fn();
    render(
      <IssuesPopover
        warnings={[]}
        errors={[]}
        issues={found}
        lineOf={lineOf}
        onRevealIssue={onRevealIssue}
        onRevealStep={onRevealStep}
      />
    );
    const panel = openPopover();
    fireEvent.click(
      within(panel).getByRole('button', { name: /Action is empty/ })
    );
    expect(onRevealIssue).toHaveBeenCalledWith(found[1]);
    expect(onRevealStep).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();
  });

  // A row is a button only when a callback of the host can show it.
  it('keeps a row text when no callback can show it', () => {
    const fileIssue: ValidationIssue = {
      severity: ValidationIssueSeverity.error,
      source: ValidationIssueSource.build,
      message: 'The file has a tab.',
      line: 2,
    };
    const { unmount } = render(
      <IssuesPopover
        warnings={[]}
        errors={[]}
        issues={[fileIssue]}
        lineOf={lineOf}
        onRevealStep={vi.fn()}
      />
    );
    let panel = openPopover();
    expect(within(panel).getByText('The file has a tab.')).toBeVisible();
    expect(
      within(panel).queryByRole('button', { name: /has a tab/ })
    ).toBeNull();
    unmount();

    // A value reference names its step only: onRevealIssue cannot show it.
    render(
      <IssuesPopover
        warnings={[]}
        errors={['an error']}
        valueReferences={[unknownStep]}
        issues={issues}
        onRevealIssue={vi.fn()}
      />
    );
    panel = openPopover();
    const group = within(panel).getByRole('region', {
      name: 'Value references (1)',
    });
    expect(within(group).getByText(/fetch does not exist/)).toBeVisible();
    expect(
      within(group).queryByRole('button', { name: /fetch does not exist/ })
    ).toBeNull();
  });

  it('reveals the step of its own issue when two steps share a message', () => {
    const onRevealStep = vi.fn();
    const shared = (stepName: string, stepPath: string): ValidationIssue => ({
      severity: ValidationIssueSeverity.error,
      source: ValidationIssueSource.build,
      message: 'id is required when the step declares outputs.',
      stepName,
      stepPath,
    });
    render(
      <IssuesPopover
        warnings={[]}
        errors={[]}
        issues={[shared('a', 'steps[0]'), shared('b', 'steps[1]')]}
        onRevealStep={onRevealStep}
      />
    );
    const panel = openPopover();
    const rows = within(panel).getAllByRole('button', {
      name: /id is required/,
    });
    expect(rows[1]).toHaveAttribute('title', 'Show step b');
    fireEvent.click(rows[1]!);
    expect(onRevealStep).toHaveBeenLastCalledWith('b');
  });

  it('takes the place of Save as the triangle icon with the count next to it', () => {
    render(
      <IssuesPopover warnings={[]} errors={[]} issues={found} lineOf={lineOf} />
    );
    const slot = screen.getByRole('button', { name: 'Issues (3)' });
    // As high as Save, with the icon and the number side by side, in the red
    // of the trash button: the destructive token.
    expect(slot).toHaveClass('h-7', 'text-destructive', 'border-destructive');
    expect(slot).not.toHaveClass('w-7');
    expect(slot).toHaveTextContent(/^3$/);
    expect(slot.querySelector('svg')).not.toBeNull();
    expect(slot.querySelector('[class*="bg-destructive"]')).toBeNull();
    fireEvent.click(slot);
    expect(screen.getByRole('dialog', { name: 'Issues' })).toBeVisible();
  });

  it('opens when the host asks, and only for a new request', () => {
    const { rerender } = render(
      <IssuesPopover warnings={[]} errors={['w']} openRequest={0} />
    );
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();
    rerender(<IssuesPopover warnings={[]} errors={['w']} openRequest={1} />);
    expect(screen.getByRole('dialog', { name: 'Issues' })).toBeVisible();
    fireEvent.keyDown(document, { key: 'Escape' });
    rerender(<IssuesPopover warnings={[]} errors={['w']} openRequest={1} />);
    expect(screen.queryByRole('dialog', { name: 'Issues' })).toBeNull();
  });
});

// The owner asked for a quick fix on the rows that have one, and for Fix all.
describe('IssuesPopover quick fixes', () => {
  const missingId = (stepPath: string, stepName: string): ValidationIssue => ({
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message: 'id is required when the step declares outputs.',
    stepPath,
    stepName,
  });
  const other: ValidationIssue = {
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message: 'Action is empty.',
    stepPath: 'steps[2]',
    stepName: 'c',
  };
  const fixOf = (id: string) => ({
    message: 'Add id: {id}',
    params: { id },
    edits: [{ from: 0, to: 0, insert: `id: ${id}` }],
  });
  const a = missingId('steps[0]', 'a');
  const b = missingId('steps[1]', 'b');
  const fixes = new Map([
    [a, fixOf('show_title')],
    [b, fixOf('show_title_1')],
  ]);

  function renderFixable(list: ValidationIssue[], onQuickFix = vi.fn()) {
    render(
      <IssuesPopover
        warnings={[]}
        errors={list.map((issue) => issue.message)}
        issues={list}
        quickFixOf={(issue) => fixes.get(issue)}
        onQuickFix={onQuickFix}
      />
    );
    openPopover();
    return onQuickFix;
  }

  it('shows a Quick fix button only on the rows that have a fix', () => {
    renderFixable([a, other]);
    const buttons = screen.getAllByRole('button', { name: /^Quick fix:/ });
    expect(buttons).toHaveLength(1);
    expect(buttons[0]).toHaveAccessibleName('Quick fix: Add id: show_title');
    expect(buttons[0]).toHaveAttribute('title', 'Add id: show_title');
  });

  it('applies the fix of its row alone', () => {
    const onQuickFix = renderFixable([a, other]);
    fireEvent.click(screen.getByRole('button', { name: /^Quick fix:/ }));
    expect(onQuickFix).toHaveBeenCalledTimes(1);
    expect(onQuickFix).toHaveBeenCalledWith([fixes.get(a)]);
  });

  it('shows no button without a fix, and no Fix all', () => {
    renderFixable([other]);
    expect(screen.queryByRole('button', { name: /^Quick fix:/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Fix all/ })).toBeNull();
  });

  it('shows no Fix all for one fix', () => {
    renderFixable([a, other]);
    expect(screen.queryByRole('button', { name: /^Fix all/ })).toBeNull();
  });

  it('shows Fix all above the groups for two fixes, and applies them in one call', () => {
    const onQuickFix = renderFixable([a, b, other]);
    const all = screen.getByRole('button', { name: /^Fix all/ });
    expect(all).toHaveTextContent('Fix all (2)');
    const panel = screen.getByRole('dialog', { name: 'Issues' });
    // It is the first thing of the panel.
    expect(panel.firstElementChild).toContainElement(all);
    fireEvent.click(all);
    expect(onQuickFix).toHaveBeenCalledTimes(1);
    expect(onQuickFix).toHaveBeenCalledWith([fixes.get(a), fixes.get(b)]);
  });

  it('counts one fix once when it serves two issues', () => {
    const warning: ValidationIssue = {
      ...a,
      severity: ValidationIssueSeverity.warning,
      message: 'The step has no id, as the other steps have.',
    };
    const shared = fixOf('x');
    const map = new Map([
      [a, shared],
      [warning, shared],
    ]);
    render(
      <IssuesPopover
        warnings={[warning.message]}
        errors={[a.message]}
        issues={[a, warning]}
        quickFixOf={(issue) => map.get(issue)}
        onQuickFix={vi.fn()}
      />
    );
    openPopover();
    expect(screen.getAllByRole('button', { name: /^Quick fix:/ })).toHaveLength(
      2
    );
    expect(screen.queryByRole('button', { name: /^Fix all/ })).toBeNull();
  });
});
