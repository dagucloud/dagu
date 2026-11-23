// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import { translateStatic } from '@/i18n/translateStatic';
import { readRetry } from '../retryPolicy';
import { buildCanvasGraph } from '../graph';
import { loopArcs } from '../loopArcs';
import { LoopArcs } from '../nodes/LoopArcs';
import type { CanvasNodeWithRoutes } from '../types';
import { makeModel, makeNames, makeStep } from './fixtures';

const ts = (text: string, values?: Record<string, string | number>) =>
  translateStatic('en', text, values);

function plain(
  extra: Partial<CanvasNodeWithRoutes> = {}
): CanvasNodeWithRoutes {
  return {
    key: 'steps[0]',
    label: 'fetch',
    kind: 'run',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    ...extra,
  };
}

describe('loopArcs', () => {
  it('has no arc for a step with no loop', () => {
    expect(loopArcs(ts, plain())).toEqual([]);
  });

  it('draws the retry with its most tries', () => {
    const retry = readRetry({ limit: 12, interval_sec: 5 });
    const [arc] = loopArcs(ts, plain({ retry }));
    expect(arc).toMatchObject({
      kind: 'retry',
      label: 'max 12',
    });
    expect(arc?.title).toBe(
      'Retries the step up to 12 times.\nWaits 5s between the attempts.'
    );
  });

  it('draws the repeat with its limit, or with the sign of no limit', () => {
    const limited = readRepeatPolicy({
      repeat: 'until',
      limit: 8,
      interval_sec: 10,
    });
    const [until] = loopArcs(ts, plain({ repeat: limited! }));
    expect(until).toMatchObject({
      kind: 'repeat',
      label: 'max 8',
    });
    const endless = readRepeatPolicy({ repeat: true, interval_sec: 30 });
    const [forever] = loopArcs(ts, plain({ repeat: endless! }));
    expect(forever).toMatchObject({ kind: 'repeat', label: '∞' });
  });

  it('draws both loops of a step, the retry first', () => {
    const arcs = loopArcs(
      ts,
      plain({
        retry: readRetry({ limit: 3 })!,
        repeat: readRepeatPolicy({ repeat: 'while', limit: 5 })!,
      })
    );
    expect(arcs.map((arc) => [arc.kind, arc.label])).toEqual([
      ['retry', 'max 3'],
      ['repeat', 'max 5'],
    ]);
  });

  // A limit that a reference gives is not a number of tries: the arc has no label.
  it('has no label for a limit that a reference gives, and keeps the arc', () => {
    const retry = readRetry({ limit: '${TRIES}' });
    const [arc] = loopArcs(ts, plain({ retry: retry! }));
    expect(arc).toMatchObject({ kind: 'retry', label: '' });
  });

  it('writes a large number of tries in the short form', () => {
    const [arc] = loopArcs(ts, plain({ retry: readRetry({ limit: 1200 })! }));
    expect(arc?.label).toBe('max 1.2k');
  });

  // The server takes a limit and a wait as text and resolves them when the
  // step runs: digits are a number, anything else is a reference.
  it('reads a limit and a wait that the YAML gives as text', () => {
    expect(readRetry({ limit: '3', interval_sec: '10' })).toEqual({
      limit: 3,
      intervalSec: 10,
    });
    const [arc] = loopArcs(
      ts,
      plain({ retry: readRetry({ limit: '$TRIES', interval_sec: '$WAIT' })! })
    );
    expect(arc).toMatchObject({ kind: 'retry', label: '' });
    expect(arc?.title).toContain('up to $TRIES times');
  });

  // The limit of a repeat can be a reference too: it is not "no limit".
  it('has no label for a repeat whose limit a reference gives', () => {
    const repeat = readRepeatPolicy({ repeat: 'while', limit: '${N}' });
    const [arc] = loopArcs(ts, plain({ repeat: repeat! }));
    expect(arc).toMatchObject({ kind: 'repeat', label: '' });
    expect(arc?.title).toContain('max ${N}');
  });

  // A limit of 0 never retries.
  it('has no arc for a retry with a limit of 0', () => {
    expect(loopArcs(ts, plain({ retry: readRetry({ limit: 0 })! }))).toEqual(
      []
    );
  });

  it('says that the wait grows when the policy backs off', () => {
    const retry = readRetry({ limit: 3, interval_sec: 5, backoff: 2 });
    const [arc] = loopArcs(ts, plain({ retry: retry! }));
    expect(arc?.title).toContain(
      'Waits 5s before the second attempt, then longer each time.'
    );
  });

  // "0" is the number 0, so the repeat has no limit.
  it('draws the sign of no limit for a repeat whose limit is "0"', () => {
    const repeat = readRepeatPolicy({ repeat: 'while', limit: '0' });
    const [arc] = loopArcs(ts, plain({ repeat: repeat! }));
    expect(arc).toMatchObject({ kind: 'repeat', label: '∞' });
    expect(arc?.title).toContain('no limit');
  });
});

describe('the retry of a step in the graph of the canvas', () => {
  function nodeOf(value: Record<string, unknown>): CanvasNodeWithRoutes {
    const model = makeModel([
      makeStep({ key: 'steps[0]', explicitName: 'a', kind: 'run', value }),
    ]);
    const graph = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
    return graph.nodes[0] as CanvasNodeWithRoutes;
  }

  it('reads the retry policy of a step', () => {
    const node = nodeOf({
      run: 'echo',
      retry_policy: { limit: 4, interval_sec: 2 },
    });
    expect(node.retry).toEqual({ limit: 4, intervalSec: 2 });
    expect(nodeOf({ run: 'echo' }).retry).toBeUndefined();
  });
});

describe('the arcs on a card', () => {
  const withBoth = plain({
    retry: readRetry({ limit: 12 })!,
    repeat: readRepeatPolicy({ repeat: 'while', limit: 8 })!,
  });

  it('draws each arc with its label and its tooltip', () => {
    render(<LoopArcs node={withBoth} />);
    const arcs = screen.getAllByTestId('ve-loop-arc');
    expect(arcs.map((arc) => [arc.dataset.kind, arc.textContent])).toEqual([
      ['retry', 'max 12'],
      ['repeat', 'max 8'],
    ]);
    expect(arcs[0]).toHaveAccessibleName(/Retries the step up to 12 times/);
    expect(arcs[0]).toHaveAttribute(
      'title',
      expect.stringContaining('Retries the step up to 12 times')
    );
    // The repeat is dashed: it loops after a run, the retry after a failure.
    expect(arcs[0]!.querySelector('svg')).not.toHaveAttribute(
      'stroke-dasharray'
    );
    expect(arcs[1]!.querySelector('svg')).toHaveAttribute('stroke-dasharray');
  });

  // The right of the upper edge holds the toolbar of the step and its issue
  // badges, and the middle a handle: the arcs sit side by side at the left.
  it('sets the arcs side by side at the left of the upper edge', () => {
    render(<LoopArcs node={withBoth} />);
    const arcs = screen.getAllByTestId('ve-loop-arc');
    expect(arcs[0]!.style.left).toBe('22px');
    expect(arcs[1]!.style.left).toBe('46.4px');
  });

  it('draws no arc on a compact card', () => {
    const { container } = render(<LoopArcs node={withBoth} compact />);
    expect(container).toBeEmptyDOMElement();
  });

  // Only the theme tokens: a literal colour breaks the dark theme.
  it('uses theme tokens only, and the colour of the links for the stroke', () => {
    const { container } = render(<LoopArcs node={withBoth} />);
    expect(container.innerHTML).not.toMatch(/#[0-9a-f]{3,8}\b|rgb\(|hsl\(/i);
    for (const svg of container.querySelectorAll('svg')) {
      expect(svg).toHaveClass('text-muted-foreground');
      expect(svg).toHaveAttribute('stroke', 'currentColor');
    }
  });

  it('draws nothing for a step with no loop', () => {
    const { container } = render(<LoopArcs node={plain()} />);
    expect(container).toBeEmptyDOMElement();
  });
});
