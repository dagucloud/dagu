// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus } from '@/api/v1/schema';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
  StepRunState,
} from '../../contracts';
import { buildExportSvg, downloadPng, downloadSvg } from '../exportCanvas';
import { NODE_HEIGHT, NODE_WIDTH, layoutGraph, routerSize } from '../layout';

function node(key: string, extra: Partial<CanvasNode> = {}): CanvasNode {
  return {
    key,
    label: key,
    kind: 'run',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    ...extra,
  };
}

function edge(
  source: string,
  target: string,
  kind: CanvasEdge['kind'] = 'depends',
  label?: string
): CanvasEdge {
  return { id: `${kind}:${source}->${target}`, source, target, kind, label };
}

function graphOf(nodes: CanvasNode[], edges: CanvasEdge[] = []): CanvasGraph {
  return { nodes, edges, editableEdges: true };
}

function run(status: NodeStatus): StepRunState {
  return { status, statusLabel: 'x', retryCount: 0, doneCount: 0 };
}

function parse(svg: string): Document {
  const doc = new DOMParser().parseFromString(svg, 'image/svg+xml');
  expect(doc.getElementsByTagName('parsererror')).toHaveLength(0);
  return doc;
}

function nodeEl(doc: Document, key: string): Element {
  const el = doc.querySelector(`[data-node="${key}"]`);
  if (!el) {
    throw new Error(`no node ${key}`);
  }
  return el;
}

const ONE = { a: { x: 0, y: 0 } };

describe('buildExportSvg', () => {
  it('is a standalone well-formed SVG with a solid background', () => {
    const graph = graphOf(
      [node('a', { subtitle: 'echo hi' }), node('b')],
      [edge('a', 'b')]
    );
    const positions = layoutGraph(graph, 'LR');
    const { svg } = buildExportSvg(graph, positions, 'LR');
    const doc = parse(svg);
    const root = doc.documentElement;
    expect(root.tagName).toBe('svg');
    expect(root.getAttribute('xmlns')).toBe('http://www.w3.org/2000/svg');
    expect(svg).not.toMatch(/var\(--|<style|https?:\/\/(?!www\.w3\.org)/);
    const background = root.querySelector(':scope > rect');
    expect(background?.getAttribute('fill')).toBe('#ffffff');
    expect(background?.getAttribute('width')).toBe(root.getAttribute('width'));
  });

  it('adds a 24px margin around all nodes', () => {
    const graph = graphOf([node('a'), node('b')]);
    const positions = { a: { x: 100, y: 50 }, b: { x: 500, y: 300 } };
    const { svg, width, height } = buildExportSvg(graph, positions, 'LR');
    expect(width).toBe(500 + NODE_WIDTH - 100 + 48);
    expect(height).toBe(300 + NODE_HEIGHT - 50 + 48);
    const root = parse(svg).documentElement;
    expect(root.getAttribute('width')).toBe(String(width));
    expect(root.getAttribute('height')).toBe(String(height));
    expect(root.getAttribute('viewBox')).toBe(`0 0 ${width} ${height}`);
    expect(svg).toContain('translate(-76 -26)');
  });

  it('grows for a router, a stack and a handler caption', () => {
    const router = node('r', { kind: 'router', routes: ['a', 'b', 'c', 'd'] });
    const stack = node('s', { kind: 'call', parallel: true });
    const handler = node('h', { lane: 'handler' });
    const positions = {
      r: { x: 0, y: 0 },
      s: { x: 0, y: 400 },
      h: { x: 400, y: 0 },
    };
    const { height, width } = buildExportSvg(
      graphOf([router, stack, handler]),
      positions,
      'LR'
    );
    // Stack ghost (+8) below, handler caption (8) above, 24px margins.
    expect(height).toBe(400 + NODE_HEIGHT + 8 + 8 + 48);
    expect(width).toBe(400 + NODE_WIDTH + 48);
    expect(routerSize(4)).toBeLessThan(400);
  });

  it('gives a small valid SVG for an empty graph', () => {
    const { svg, width, height } = buildExportSvg(graphOf([]), {}, 'LR');
    expect([width, height]).toEqual([48, 48]);
    parse(svg);
  });

  it('skips nodes without a position and edges to them', () => {
    const graph = graphOf([node('a'), node('b')], [edge('a', 'b')]);
    const { svg } = buildExportSvg(graph, ONE, 'LR');
    const doc = parse(svg);
    expect(doc.querySelectorAll('[data-node]')).toHaveLength(1);
    expect(doc.querySelectorAll('[data-edge]')).toHaveLength(0);
  });

  it('escapes text and drops characters that XML forbids', () => {
    const graph = graphOf([
      node('a', {
        label: `<b> & "q" 'x'\u0001`,
        subtitle: 'echo "a" && <c>',
      }),
    ]);
    const { svg } = buildExportSvg(graph, ONE, 'LR');
    expect(svg).not.toContain('<b>');
    expect(svg).toContain('&lt;b&gt; &amp; &quot;q&quot; &apos;x&apos;');
    const doc = parse(svg);
    const texts = [...nodeEl(doc, 'a').querySelectorAll('text')].map(
      (t) => t.textContent
    );
    expect(texts).toEqual([`<b> & "q" 'x'`, 'echo "a" && <c>']);
  });

  it('escapes a key in an attribute', () => {
    const graph = graphOf([node('a"<b')]);
    const { svg } = buildExportSvg(graph, { 'a"<b': { x: 0, y: 0 } }, 'LR');
    parse(svg);
  });

  it('truncates a long label and subtitle with an ellipsis', () => {
    const graph = graphOf([
      node('a', { label: 'L'.repeat(80), subtitle: 's'.repeat(80) }),
    ]);
    const doc = parse(buildExportSvg(graph, ONE, 'LR').svg);
    const [label, subtitle] = [...nodeEl(doc, 'a').querySelectorAll('text')];
    expect(label?.textContent?.endsWith('…')).toBe(true);
    expect(label?.textContent?.length).toBeLessThan(40);
    expect(subtitle?.textContent?.endsWith('…')).toBe(true);
    expect(subtitle?.textContent?.length).toBeLessThan(40);
  });

  it('keeps a short label whole', () => {
    const doc = parse(buildExportSvg(graphOf([node('a')]), ONE, 'LR').svg);
    expect(nodeEl(doc, 'a').querySelector('text')?.textContent).toBe('a');
  });

  it('draws a plain step as a rounded card', () => {
    const doc = parse(buildExportSvg(graphOf([node('a')]), ONE, 'LR').svg);
    const g = nodeEl(doc, 'a');
    expect(g.getAttribute('data-shape')).toBe('card');
    const rects = g.querySelectorAll('rect');
    expect(rects).toHaveLength(1);
    expect(rects[0]?.getAttribute('width')).toBe('240');
    expect(rects[0]?.getAttribute('height')).toBe('64');
    expect(rects[0]?.getAttribute('rx')).toBe('6');
    expect(g.querySelectorAll('line')).toHaveLength(0);
    expect(rects[0]?.hasAttribute('stroke-dasharray')).toBe(false);
  });

  it('colors the border and dot of a step by its run status', () => {
    const cases: [NodeStatus, string][] = [
      [NodeStatus.Success, '#178a44'],
      [NodeStatus.Failed, '#c8382f'],
      [NodeStatus.Running, '#5d4fe0'],
      [NodeStatus.Waiting, '#9a5d0b'],
      [NodeStatus.Aborted, '#9333ea'],
      [NodeStatus.NotStarted, '#80808b'],
    ];
    for (const [status, color] of cases) {
      const graph = graphOf([node('a', { run: run(status) })]);
      const g = nodeEl(parse(buildExportSvg(graph, ONE, 'LR').svg), 'a');
      expect(g.querySelector('rect')?.getAttribute('stroke')).toBe(color);
      expect(g.querySelector('circle')?.getAttribute('fill')).toBe(color);
    }
    const dark = graphOf([node('a', { run: run(NodeStatus.Success) })]);
    const g = nodeEl(
      parse(buildExportSvg(dark, ONE, 'LR', { dark: true }).svg),
      'a'
    );
    expect(g.querySelector('rect')?.getAttribute('stroke')).toBe('#22c55e');
  });

  it('draws a sub-DAG with two vertical bars', () => {
    const graph = graphOf([node('a', { kind: 'call', subDag: 'child' })]);
    const g = nodeEl(parse(buildExportSvg(graph, ONE, 'LR').svg), 'a');
    expect(g.getAttribute('data-shape')).toBe('subroutine');
    const lines = [...g.querySelectorAll('line')];
    expect(lines.map((l) => l.getAttribute('x1'))).toEqual(['8', '232']);
    for (const line of lines) {
      expect(line.getAttribute('x1')).toBe(line.getAttribute('x2'));
      expect(line.getAttribute('y2')).toBe('64');
    }
    expect(g.querySelectorAll('rect')).toHaveLength(1);
    // The sub-DAG name stands in for a missing subtitle.
    expect([...g.querySelectorAll('text')].map((t) => t.textContent)).toContain(
      'child'
    );
  });

  it('draws two ghost cards behind a fan-out of sub-DAGs', () => {
    const graph = graphOf([node('a', { kind: 'call', parallel: true })]);
    const g = nodeEl(parse(buildExportSvg(graph, ONE, 'LR').svg), 'a');
    expect(g.getAttribute('data-shape')).toBe('stack');
    const rects = [...g.querySelectorAll('rect')];
    expect(
      rects.map((r) => [r.getAttribute('x'), r.getAttribute('y')])
    ).toEqual([
      ['8', '8'],
      ['4', '4'],
      ['0', '0'],
    ]);
    expect(g.querySelectorAll('line')).toHaveLength(2);
  });

  it('does not stack a parallel step that is not a sub-DAG', () => {
    const graph = graphOf([node('a', { parallel: true })]);
    const g = nodeEl(parse(buildExportSvg(graph, ONE, 'LR').svg), 'a');
    expect(g.querySelectorAll('rect')).toHaveLength(1);
  });

  it('draws a router as a diamond with its route value', () => {
    const router = node('a', {
      kind: 'router',
      routeValue: '${x}',
      routes: ['yes', 'no'],
    });
    const g = nodeEl(
      parse(buildExportSvg(graphOf([router]), ONE, 'LR').svg),
      'a'
    );
    expect(g.getAttribute('data-shape')).toBe('diamond');
    // A square turned by 45 degrees: both diagonals have the same length.
    const h = routerSize(2);
    expect(g.querySelector('polygon')?.getAttribute('points')).toBe(
      `${h / 2},0 ${h},${h / 2} ${h / 2},${h} 0,${h / 2}`
    );
    expect(g.querySelector('rect')).toBeNull();
    const texts = [...g.querySelectorAll('text')].map((t) => t.textContent);
    expect(texts).toEqual(['a', '${x}', 'yes', 'no']);
  });

  it('recognizes a router by its action', () => {
    const graph = graphOf([node('a', { action: 'router.route' })]);
    const g = nodeEl(parse(buildExportSvg(graph, ONE, 'LR').svg), 'a');
    expect(g.querySelector('polygon')).not.toBeNull();
  });

  it('writes a port pattern only when the route has no edge', () => {
    const router = node('r', { kind: 'router', routes: ['yes', 'no'] });
    const graph = graphOf(
      [router, node('t')],
      [edge('r', 't', 'route', 'yes')]
    );
    const positions = { r: { x: 0, y: 0 }, t: { x: 400, y: 0 } };
    const doc = parse(buildExportSvg(graph, positions, 'LR').svg);
    const texts = [...nodeEl(doc, 'r').querySelectorAll('text')].map(
      (t) => t.textContent
    );
    expect(texts).toEqual(['r', 'no']);
  });

  it('draws a handler with a dashed border and a caption', () => {
    const graph = graphOf([node('a', { lane: 'handler' })]);
    const g = nodeEl(parse(buildExportSvg(graph, ONE, 'LR').svg), 'a');
    const card = [...g.querySelectorAll('rect')].find(
      (r) => r.getAttribute('height') === '64'
    );
    expect(card?.getAttribute('stroke-dasharray')).toBe('4 3');
    expect([...g.querySelectorAll('text')].map((t) => t.textContent)).toContain(
      'Handler'
    );
  });

  it('styles each edge kind', () => {
    const nodes = ['a', 'b', 'c', 'd', 'r'].map((k) =>
      k === 'r' ? node(k, { kind: 'router', routes: ['ok'] }) : node(k)
    );
    const graph = graphOf(nodes, [
      edge('a', 'b', 'depends'),
      edge('a', 'c', 'inferred'),
      edge('a', 'd', 'order'),
      edge('r', 'b', 'route', 'ok'),
      edge('a', 'r', 'agent'),
    ]);
    const positions = layoutGraph(graph, 'LR');
    const doc = parse(buildExportSvg(graph, positions, 'LR').svg);
    const path = (id: string) => {
      const el = doc.querySelector(`[data-edge="${id}"]`);
      if (!el) {
        throw new Error(id);
      }
      return el;
    };
    const depends = path('depends:a->b');
    expect(depends.hasAttribute('stroke-dasharray')).toBe(false);
    expect(depends.getAttribute('marker-end')).toBe('url(#ve-arrow)');
    expect(depends.getAttribute('stroke')).toBe('#80808b');
    expect(path('inferred:a->c').getAttribute('stroke-dasharray')).toBe('6 4');
    const order = path('order:a->d');
    expect(order.getAttribute('stroke-dasharray')).toBe('2 4');
    expect(order.getAttribute('marker-end')).toBe('url(#ve-arrow-muted)');
    expect(order.getAttribute('stroke')).not.toBe(
      depends.getAttribute('stroke')
    );
    // The tools hang from the agent without a line, as on the canvas.
    expect(doc.querySelector('[data-edge="agent:a->r"]')).toBeNull();
    const route = path('route:r->b');
    expect(route.hasAttribute('stroke-dasharray')).toBe(false);
    expect(doc.getElementById('ve-arrow')).not.toBeNull();
    expect(doc.getElementById('ve-arrow-muted')).not.toBeNull();
  });

  // The export follows the canvas: straight or orthogonal, no curve, and
  // the arrowhead tip on the end of the line.
  it('draws edges with straight segments and a tip on the line end', () => {
    const graph = graphOf([node('a'), node('b')], [edge('a', 'b', 'depends')]);
    const positions = layoutGraph(graph, 'TB');
    const doc = parse(buildExportSvg(graph, positions, 'TB').svg);
    for (const el of Array.from(doc.querySelectorAll('[data-edge]'))) {
      expect(el.getAttribute('d')).not.toMatch(/C/);
    }
    const aligned = doc.querySelector('[data-edge="depends:a->b"]')!;
    expect(aligned.getAttribute('d')).toMatch(
      /^M[\d.]+,[\d.]+ L[\d.]+,[\d.]+$/
    );
    expect(doc.getElementById('ve-arrow')?.getAttribute('refX')).toBe('10');
  });

  it('writes the pattern of a route edge at its middle', () => {
    const router = node('r', { kind: 'router', routes: ['a<b'] });
    const graph = graphOf(
      [router, node('t')],
      [edge('r', 't', 'route', 'a<b'), edge('r', 't', 'depends')]
    );
    const positions = { r: { x: 0, y: 0 }, t: { x: 400, y: 0 } };
    const doc = parse(buildExportSvg(graph, positions, 'LR').svg);
    const labels = doc.querySelectorAll('[data-edge-label]');
    expect(labels).toHaveLength(1);
    expect(labels[0]?.getAttribute('data-edge-label')).toBe('route:r->t');
    expect(labels[0]?.querySelector('text')?.textContent).toBe('a<b');
    expect(labels[0]?.querySelector('rect')).not.toBeNull();
  });

  it('starts a route edge on the diamond port of its pattern', () => {
    const router = node('r', { kind: 'router', routes: ['x', 'y'] });
    const graph = graphOf(
      [router, node('t')],
      [edge('r', 't', 'route', 'x'), edge('r', 't', 'route', 'y')]
    );
    const positions = { r: { x: 0, y: 0 }, t: { x: 400, y: 0 } };
    const doc = parse(buildExportSvg(graph, positions, 'LR').svg);
    const start = (id: string) =>
      doc
        .querySelector(`[data-edge="${id}"]`)
        ?.getAttribute('d')
        ?.split(' ')[0];
    expect(start('route:r->t')).toBeDefined();
    const starts = [...doc.querySelectorAll('[data-edge]')].map(
      (e) => e.getAttribute('d')?.split(' ')[0]
    );
    expect(new Set(starts).size).toBe(2);
    // The ports sit on the right edges of the diamond, left of its right vertex.
    for (const s of starts) {
      const x = Number(s?.slice(1).split(',')[0]);
      expect(x).toBeLessThan(routerSize(2));
      expect(x).toBeGreaterThan(routerSize(2) / 2);
    }
  });

  it('leaves from the right and arrives on the left in LR', () => {
    const graph = graphOf([node('a'), node('b')], [edge('a', 'b')]);
    const positions = { a: { x: 0, y: 0 }, b: { x: 400, y: 100 } };
    const d = parse(buildExportSvg(graph, positions, 'LR').svg)
      .querySelector('[data-edge]')
      ?.getAttribute('d');
    // A smooth curve with horizontal tangents, ending on the outer edge of
    // the entry dot (4 px before the card), not on its centre.
    expect(d).toMatch(/^M240,32 C\d+,32 \d+,132 396,132$/);
  });

  it('leaves from the bottom and arrives on the top in TB', () => {
    const graph = graphOf([node('a'), node('b')], [edge('a', 'b')]);
    const positions = { a: { x: 0, y: 0 }, b: { x: 100, y: 300 } };
    const d = parse(buildExportSvg(graph, positions, 'TB').svg)
      .querySelector('[data-edge]')
      ?.getAttribute('d');
    // A smooth curve with vertical tangents, ending on the outer edge of the
    // entry dot, 4 px above the top of the target.
    expect(d).toMatch(/^M120,64 C120,\d+ 220,\d+ 220,296$/);
  });

  it('leaves from the lower vertex of a router in TB', () => {
    const router = node('r', { kind: 'router' });
    const graph = graphOf([router, node('t')], [edge('r', 't')]);
    const positions = { r: { x: 0, y: 0 }, t: { x: 0, y: 400 } };
    const d = parse(buildExportSvg(graph, positions, 'TB').svg)
      .querySelector('[data-edge]')
      ?.getAttribute('d');
    expect(d).toMatch(
      new RegExp(
        `^M${routerSize(0) / 2},${routerSize(0)} C${routerSize(0) / 2},`
      )
    );
  });

  // The same curve as the canvas: through the waypoints of the layout, and
  // around a card that the edge does not link.
  it('draws a long edge through its waypoints', () => {
    const graph = graphOf([node('a'), node('b')], [edge('a', 'b')]);
    const positions = { a: { x: 0, y: 0 }, b: { x: 0, y: 400 } };
    const d = parse(
      buildExportSvg(graph, positions, 'TB', {
        waypoints: {
          'depends:a->b': [
            { x: 300, y: 100 },
            { x: 300, y: 300 },
          ],
        },
      }).svg
    )
      .querySelector('[data-edge]')
      ?.getAttribute('d');
    expect(d).toContain(' L300,300');
    expect(d).toMatch(/ 120,396$/);
  });

  it('goes around a card between the ends of an edge', () => {
    const graph = graphOf([node('a'), node('b'), node('c')], [edge('a', 'c')]);
    const positions = {
      a: { x: 0, y: 0 },
      b: { x: 0, y: 200 },
      c: { x: 0, y: 400 },
    };
    const d = parse(buildExportSvg(graph, positions, 'TB').svg)
      .querySelector('[data-edge]')
      ?.getAttribute('d');
    // A straight line would run through b: the path bends aside instead.
    expect(d).toMatch(/C/);
    const xs = [...(d ?? '').matchAll(/ (-?\d+(?:\.\d+)?),\d/g)].map((m) =>
      Number(m[1])
    );
    expect(xs.some((x) => x < -6 || x > 246)).toBe(true);
  });

  // In TB a route label starts right of the middle of its edge. Under a
  // centred router it reaches past the right side of the target card.
  it('keeps a route label inside the image', () => {
    const pattern = 'x'.repeat(40);
    const graph = graphOf(
      [node('r', { kind: 'router', routes: [pattern] }), node('t')],
      [edge('r', 't', 'route', pattern)]
    );
    const positions = {
      r: { x: (NODE_WIDTH - routerSize(1)) / 2, y: 0 },
      t: { x: 0, y: 300 },
    };
    const { svg, width } = buildExportSvg(graph, positions, 'TB');
    const doc = parse(svg);
    const shift = Number(/translate\((-?[\d.]+) /.exec(svg)?.[1] ?? Number.NaN);
    const rect = doc.querySelector('[data-edge-label] rect')!;
    const right =
      shift +
      Number(rect.getAttribute('x')) +
      Number(rect.getAttribute('width'));
    expect(right).toBeLessThanOrEqual(width);
  });

  it('uses a dark palette that differs from the light one', () => {
    const graph = graphOf([node('a'), node('b')], [edge('a', 'b')]);
    const positions = layoutGraph(graph, 'LR');
    const light = buildExportSvg(graph, positions, 'LR');
    const dark = buildExportSvg(graph, positions, 'LR', { dark: true });
    expect(dark.svg).not.toBe(light.svg);
    expect([dark.width, dark.height]).toEqual([light.width, light.height]);
    const bg = parse(dark.svg).documentElement.querySelector(':scope > rect');
    expect(bg?.getAttribute('fill')).toBe('#15151a');
    expect(light.svg).not.toContain('#15151a');
  });

  it('gives the same string for the same input', () => {
    const graph = graphOf(
      [
        node('a', { run: run(NodeStatus.Failed) }),
        node('r', { kind: 'router', routes: ['x'] }),
        node('s', { kind: 'call', parallel: true }),
        node('h', { lane: 'handler' }),
      ],
      [edge('a', 'r'), edge('r', 's', 'route', 'x')]
    );
    const positions = layoutGraph(graph, 'LR');
    const first = buildExportSvg(graph, positions, 'LR').svg;
    expect(buildExportSvg(graph, positions, 'LR').svg).toBe(first);
    expect(first).not.toMatch(/\d{6,}/);
  });
});

// The owner asked for the name under the compact card, always: the export
// draws the caption too, and its handles stay on the sides of the box.
describe('buildExportSvg of a compact canvas', () => {
  const graph = graphOf(
    [node('a', { label: 'extract orders' }), node('b', { label: '' })],
    [edge('a', 'b')]
  );

  it('draws the caption under each card, and the card in the box', () => {
    for (const direction of ['TB', 'LR'] as const) {
      const positions = layoutGraph(graph, direction, undefined, true);
      const { svg } = buildExportSvg(graph, positions, direction, {
        compact: true,
      });
      const doc = parse(svg);
      const first = nodeEl(doc, 'a');
      const texts = Array.from(first.querySelectorAll('text')).map(
        (t) => t.textContent
      );
      // The initials in the card, and the name under it.
      expect(texts).toContain('EX');
      expect(texts).toContain('extract orders');
      const caption = Array.from(first.querySelectorAll('text')).find(
        (t) => t.textContent === 'extract orders'
      )!;
      expect(caption.getAttribute('text-anchor')).toBe('middle');
      expect(Number(caption.getAttribute('y'))).toBeGreaterThan(56);
      // The card is the 56 px square, and it is not the whole box.
      const card = first.querySelector('rect')!;
      expect(card.getAttribute('width')).toBe('56');
      expect(card.getAttribute('height')).toBe('56');
    }
  });

  it('cuts a long name with an ellipsis', () => {
    const long = graphOf([
      node('a', { label: 'a very long step name indeed' }),
    ]);
    const { svg } = buildExportSvg(long, ONE, 'TB', { compact: true });
    const caption = Array.from(
      nodeEl(parse(svg), 'a').querySelectorAll('text')
    ).pop()!;
    expect(caption.textContent?.endsWith('…')).toBe(true);
  });

  it('keeps the caption inside the image', () => {
    const { svg, width, height } = buildExportSvg(
      graph,
      { a: { x: 0, y: 0 } },
      'LR',
      {
        compact: true,
      }
    );
    expect(parse(svg).documentElement.getAttribute('width')).toBe(
      String(width)
    );
    // 76 px of box and 10 px of caption below it, the 24 px margins, and
    // the caption of 120 px that hangs 32 px out of the 56 px box.
    expect(height).toBeGreaterThanOrEqual(86 + 48 - 1);
    expect(width).toBeGreaterThanOrEqual(120 + 48 - 1);
  });
});

describe('download', () => {
  let clicked: { download: string; href: string }[];
  let createObjectURL: ReturnType<typeof vi.fn>;
  let revokeObjectURL: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    clicked = [];
    createObjectURL = vi.fn(() => 'blob:fake');
    revokeObjectURL = vi.fn();
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      writable: true,
      value: createObjectURL,
    });
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      writable: true,
      value: revokeObjectURL,
    });
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement
    ) {
      clicked.push({ download: this.download, href: this.href });
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('downloadSvg clicks a link to a Blob named after the base name', () => {
    downloadSvg('<svg xmlns="http://www.w3.org/2000/svg"/>', 'flow');
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    const blob = createObjectURL.mock.calls[0]?.[0] as Blob;
    expect(blob).toBeInstanceOf(Blob);
    expect(blob.type).toContain('image/svg+xml');
    expect(clicked).toEqual([{ download: 'flow.svg', href: 'blob:fake' }]);
    expect(document.querySelector('a[download]')).toBeNull();
  });

  it('downloadSvg revokes the object URL later', async () => {
    downloadSvg('<svg/>', 'flow');
    await vi.waitFor(() =>
      expect(revokeObjectURL).toHaveBeenCalledWith('blob:fake')
    );
  });

  function mockImage(fail = false) {
    const sources: string[] = [];
    class FakeImage {
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      set src(value: string) {
        sources.push(value);
        queueMicrotask(() => (fail ? this.onerror?.() : this.onload?.()));
      }
    }
    vi.stubGlobal('Image', FakeImage);
    return sources;
  }

  it('downloadPng draws the SVG at scale and clicks a .png link', async () => {
    const sources = mockImage();
    const drawImage = vi.fn();
    const sizes: number[][] = [];
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
      function (this: HTMLCanvasElement) {
        sizes.push([this.width, this.height]);
        return { drawImage } as unknown as CanvasRenderingContext2D;
      } as never
    );
    const toBlob = vi
      .spyOn(HTMLCanvasElement.prototype, 'toBlob')
      .mockImplementation((cb: BlobCallback) =>
        cb(new Blob(['png'], { type: 'image/png' }))
      );

    await downloadPng(
      '<svg xmlns="http://www.w3.org/2000/svg"/>',
      300,
      150,
      'flow'
    );

    expect(sources[0]).toMatch(/^data:image\/svg\+xml;charset=utf-8,%3Csvg/);
    expect(sizes).toEqual([[600, 300]]);
    expect(drawImage).toHaveBeenCalledWith(expect.anything(), 0, 0, 600, 300);
    expect(toBlob).toHaveBeenCalledTimes(1);
    expect(toBlob.mock.calls[0]?.[1]).toBe('image/png');
    expect(clicked).toEqual([{ download: 'flow.png', href: 'blob:fake' }]);
  });

  it('downloadPng takes a scale', async () => {
    mockImage();
    const sizes: number[][] = [];
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
      function (this: HTMLCanvasElement) {
        sizes.push([this.width, this.height]);
        return { drawImage: vi.fn() } as unknown as CanvasRenderingContext2D;
      } as never
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(
      (cb: BlobCallback) => cb(new Blob(['png']))
    );
    await downloadPng('<svg/>', 100, 50, 'x', 1);
    expect(sizes).toEqual([[100, 50]]);
  });

  it('downloadPng rejects when the image does not load', async () => {
    mockImage(true);
    await expect(downloadPng('<svg/>', 10, 10, 'x')).rejects.toThrow();
    expect(clicked).toEqual([]);
  });

  it('downloadPng rejects when the canvas gives no PNG', async () => {
    mockImage();
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
      (() => ({ drawImage: vi.fn() })) as never
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(
      (cb: BlobCallback) => cb(null)
    );
    await expect(downloadPng('<svg/>', 10, 10, 'x')).rejects.toThrow();
    expect(clicked).toEqual([]);
  });
});
