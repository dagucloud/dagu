// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ReferenceCandidate } from '../../contracts';
import { AvailableReferences } from '../AvailableReferences';

const candidates: ReferenceCandidate[] = [
  {
    token: '${context.run.id}',
    label: 'run.id',
    group: 'context',
    known: 'runtime',
  },
  {
    token: '${params.REGION}',
    label: 'REGION',
    group: 'params',
    detail: '= eu',
    known: 'static',
  },
  {
    token: '${foreach.item}',
    label: 'item',
    group: 'foreach',
    known: 'runtime',
  },
];

describe('AvailableReferences', () => {
  it('lists every candidate by group, in the fixed group order', () => {
    render(<AvailableReferences candidates={candidates} />);
    const groups = screen.getAllByRole('group');
    expect(groups.map((group) => group.getAttribute('aria-label'))).toEqual([
      'Current item',
      'Parameters',
      'Run context',
    ]);
    const params = within(groups[1] as HTMLElement);
    expect(params.getByRole('button', { name: 'REGION' })).toHaveAttribute(
      'title',
      '${params.REGION}'
    );
    expect(params.getByText('= eu')).toBeInTheDocument();
  });

  it('marks what only a run gives, once for a whole group', () => {
    const context = ['run.id', 'run.root_id'].map(
      (label): ReferenceCandidate => ({
        token: `\${context.${label}}`,
        label,
        group: 'context',
        known: 'runtime',
      })
    );
    render(
      <AvailableReferences
        candidates={[
          ...context,
          {
            token: '${params.REGION}',
            label: 'REGION',
            group: 'params',
            known: 'static',
          },
        ]}
      />
    );
    const params = screen.getByRole('group', { name: 'Parameters' });
    const run = screen.getByRole('group', { name: 'Run context' });
    expect(within(params).queryByText('Known after run')).toBeNull();
    expect(within(run).getAllByText('Known after run')).toHaveLength(1);
  });

  it('marks each row of a group that is only partly known', () => {
    render(
      <AvailableReferences
        candidates={[
          {
            token: '${steps.a.outputs.x}',
            label: 'a.x',
            group: 'steps',
            known: 'runtime',
          },
          {
            token: '${steps.b.outputs.y}',
            label: 'b.y',
            group: 'steps',
            known: 'static',
          },
        ]}
      />
    );
    const steps = screen.getByRole('group', { name: 'Step outputs' });
    expect(within(steps).getAllByText('Known after run')).toHaveLength(1);
    const row = within(steps).getByRole('button', {
      name: 'a.x',
    }).parentElement;
    expect(row).toHaveTextContent('Known after run');
  });

  // The step view tells apart a field that finds things from one that takes a
  // value: its input column has no field for a value.
  it('searches with a search box, not a text field', () => {
    render(<AvailableReferences candidates={candidates} />);
    expect(
      screen.getByRole('searchbox', { name: 'Search references' })
    ).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('filters with the search box', () => {
    render(<AvailableReferences candidates={candidates} />);
    fireEvent.change(screen.getByLabelText('Search references'), {
      target: { value: 'region' },
    });
    expect(screen.getAllByRole('group')).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'REGION' })).toBeInTheDocument();
  });

  it('says when nothing matches', () => {
    render(<AvailableReferences candidates={candidates} />);
    fireEvent.change(screen.getByLabelText('Search references'), {
      target: { value: 'zzz' },
    });
    expect(screen.getByText('No matching references')).toBeInTheDocument();
  });

  it('shows nothing without candidates', () => {
    const { container } = render(<AvailableReferences candidates={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('inserts the token of a row', () => {
    const onInsert = vi.fn(() => true);
    render(<AvailableReferences candidates={candidates} onInsert={onInsert} />);
    fireEvent.click(screen.getByRole('button', { name: 'REGION' }));
    expect(onInsert).toHaveBeenCalledWith('${params.REGION}');
  });

  // Two steps can publish the same output variable.
  it('keeps two rows of one token apart', () => {
    const consoleError = vi
      .spyOn(console, 'error')
      .mockImplementation(() => {});
    const variable = (stepName: string): ReferenceCandidate => ({
      token: '${RESULT}',
      label: `${stepName}.RESULT`,
      group: 'steps',
      stepName,
      known: 'static',
    });
    render(<AvailableReferences candidates={[variable('a'), variable('b')]} />);
    expect(
      screen.getByRole('button', { name: 'a.RESULT' })
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'b.RESULT' })
    ).toBeInTheDocument();
    expect(consoleError).not.toHaveBeenCalled();
    consoleError.mockRestore();
  });

  it('tells a candidate that it was picked, and says what a pick does', () => {
    const onPicked = vi.fn();
    const onInsert = vi.fn(() => true);
    render(
      <AvailableReferences
        candidates={[
          {
            token: '${steps.a.outputs.stdout}',
            label: 'a.stdout',
            group: 'steps',
            known: 'runtime',
            publish: { step: 'steps[0]' },
            onPicked,
          },
        ]}
        onInsert={onInsert}
      />
    );
    expect(screen.getByText('Published when chosen')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'a.stdout' }));
    expect(onPicked).toHaveBeenCalledTimes(1);
    expect(onInsert).toHaveBeenCalledWith('${steps.a.outputs.stdout}');
  });

  // The group cannot say it for all when one row says something else.
  it('marks each row when a group also has a publishable row', () => {
    render(
      <AvailableReferences
        candidates={[
          {
            token: '${steps.a.outputs.x}',
            label: 'a.x',
            group: 'steps',
            known: 'runtime',
          },
          {
            token: '${steps.b.outputs.stdout}',
            label: 'b.stdout',
            group: 'steps',
            known: 'runtime',
            publish: { step: 'steps[1]' },
          },
        ]}
      />
    );
    const row = (name: string) =>
      screen.getByRole('button', { name }).parentElement;
    expect(row('a.x')).toHaveTextContent('Known after run');
    expect(row('b.stdout')).toHaveTextContent('Published when chosen');
    expect(screen.getAllByText('Known after run')).toHaveLength(1);
  });

  it('warns of a value that is not safe in a shell', () => {
    render(
      <AvailableReferences
        candidates={[
          {
            token: '${steps.model.outputs.reply}',
            label: 'model.reply',
            group: 'steps',
            known: 'runtime',
            shellRisk: true,
          },
        ]}
      />
    );
    expect(screen.getByText('Not safe in a shell')).toHaveAttribute(
      'title',
      expect.stringContaining('Pass it through env')
    );
  });

  it('copies the token when no field can take it', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.assign(navigator, { clipboard: { writeText } });
    render(
      <AvailableReferences candidates={candidates} onInsert={() => false} />
    );
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'REGION' }));
    });
    expect(writeText).toHaveBeenCalledWith('${params.REGION}');
  });
});
