// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import { translateStatic } from '@/i18n/translateStatic';
import type { CanvasNode } from '../../contracts';
import { readRetry } from '../agentCard';
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

  it('draws the retry on the right with its number of tries', () => {
    const retry = readRetry({ limit: 12, interval_sec: 5 });
    const [arc] = loopArcs(ts, plain({ retry }));
    expect(arc).toMatchObject({ kind: 'retry', side: 'right', label: '×12' });
    expect(arc?.title).toBe(
      'Retries the step up to 12 times.\nWaits 5s between the attempts.'
    );
  });

  it('draws the repeat on the left, with its limit or with the sign of no limit', () => {
    const limited = readRepeatPolicy({
      repeat: 'until',
      limit: 8,
      interval_sec: 10,
    });
    const [until] = loopArcs(ts, plain({ repeat: limited! }));
    expect(until).toMatchObject({ kind: 'repeat', side: 'left', label: '×8' });
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
    expect(arcs.map((arc) => [arc.kind, arc.side, arc.label])).toEqual([
      ['retry', 'right', '×3'],
      ['repeat', 'left', '×5'],
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
    expect(arc?.label).toBe('×1.2k');
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

  it('reads the retry policy of every step, not only of an agent step', () => {
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

  it('draws each arc with its label, its tooltip and its place', () => {
    render(<LoopArcs node={withBoth as CanvasNode} />);
    const arcs = screen.getAllByTestId('ve-loop-arc');
    expect(
      arcs.map((arc) => [arc.dataset.kind, arc.dataset.side, arc.textContent])
    ).toEqual([
      ['retry', 'right', '×12'],
      ['repeat', 'left', '×8'],
    ]);
    expect(arcs[0]).toHaveAccessibleName(/Retries the step up to 12 times/);
    expect(arcs[0]).toHaveAttribute('data-placement', 'side');
    // The repeat is dashed: it loops after a run, the retry after a failure.
    expect(arcs[0]!.querySelector('svg')).not.toHaveAttribute(
      'stroke-dasharray'
    );
    expect(arcs[1]!.querySelector('svg')).toHaveAttribute('stroke-dasharray');
  });

  // The arcs sit where the step starts: the upper edge of a vertical graph.
  it('hangs the arcs on the upper edge, the retry at the right corner and the repeat at the left one', () => {
    render(<LoopArcs node={withBoth as CanvasNode} placement="top" />);
    const arcs = screen.getAllByTestId('ve-loop-arc');
    expect(arcs.map((arc) => arc.dataset.placement)).toEqual(['top', 'top']);
    expect(arcs[0]).toHaveClass('top-0');
    expect(arcs[0]!.style.right).toBe('22px');
    expect(arcs[1]!.style.left).toBe('22px');
  });

  // An arc is easy to see at full zoom: 1.4 times its drawing box.
  it('draws the arc larger than its drawing box', () => {
    render(<LoopArcs node={withBoth as CanvasNode} />);
    const svg = screen.getAllByTestId('ve-loop-arc')[0]!.querySelector('svg')!;
    expect(svg).toHaveAttribute('width', '22.4');
    expect(svg).toHaveAttribute('viewBox', '0 0 16 14');
  });

  // The left side of a horizontal graph: both arcs, clear of the handle in the middle.
  it('hangs both arcs on the left side, the retry low and the repeat high', () => {
    render(<LoopArcs node={withBoth as CanvasNode} placement="side" />);
    const arcs = screen.getAllByTestId('ve-loop-arc');
    expect(arcs[0]).toHaveClass('left-0');
    expect(arcs[1]).toHaveClass('left-0');
    expect(arcs[0]!.style.bottom).toBe('8px');
    expect(arcs[1]!.style.top).toBe('8px');
  });

  // The caption of a compact card lies under it: its arcs hug the corners.
  it('hangs the arcs of a compact step close to the corners of its upper edge', () => {
    render(<LoopArcs node={withBoth as CanvasNode} placement="top" compact />);
    const arcs = screen.getAllByTestId('ve-loop-arc');
    expect(arcs.map((arc) => arc.dataset.placement)).toEqual(['top', 'top']);
    expect(arcs[0]).toHaveClass('top-0');
    expect(arcs[0]).not.toHaveClass('bottom-0');
    expect(arcs[0]!.style.right).toBe('2px');
    expect(arcs[1]!.style.left).toBe('2px');
    expect(arcs[0]!.querySelector('svg')).toHaveClass('-scale-y-100');
    expect(arcs[0]).toHaveTextContent('×12');
  });

  // Only the theme tokens: a literal colour breaks the dark theme.
  it('uses theme tokens only, and the colour of the links for the stroke', () => {
    const { container } = render(<LoopArcs node={withBoth as CanvasNode} />);
    expect(container.innerHTML).not.toMatch(/#[0-9a-f]{3,8}\b|rgb\(|hsl\(/i);
    for (const svg of container.querySelectorAll('svg')) {
      expect(svg).toHaveClass('text-muted-foreground');
      expect(svg).toHaveAttribute('stroke', 'currentColor');
    }
  });

  it('draws nothing for a step with no loop', () => {
    const { container } = render(<LoopArcs node={plain() as CanvasNode} />);
    expect(container).toBeEmptyDOMElement();
  });
});
