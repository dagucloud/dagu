// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus } from '@/api/v1/schema';
import type {
  CanvasDirection,
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
} from '../contracts';
import { routedCurve, type Obstacle } from './edgeRouting';
import {
  Position,
  curvePathData,
  curvePointAt,
  sampleCurve,
} from './engine/geometry';
import {
  COMPACT_CAPTION_HEIGHT,
  COMPACT_CAPTION_WIDTH,
  COMPACT_NODE_SIZE,
  compactCardOffset,
  hasCompactCaption,
  nodeSize,
  routePorts,
} from './layout';
import type { NodePositions } from './types';

const MARGIN = 24;
const GHOST_STEP = 4;
const HANDLER_CAPTION_RISE = 8;

const SANS =
  "system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif";
const MONO =
  "ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace";

/** Width in px that a character of each font takes, as a rough estimate. */
const LABEL_CHAR_PX = 7.4;
/** Average width of a character of the 12 px caption of a compact card. */
const CAPTION_CHAR_PX = 6.4;
const MONO_CHAR_PX = 6.7;
const SMALL_MONO_CHAR_PX = 6.1;

interface Palette {
  background: string;
  card: string;
  border: string;
  text: string;
  muted: string;
  edge: string;
  edgeMuted: string;
  success: string;
  failed: string;
  running: string;
  warning: string;
  aborted: string;
  neutral: string;
}

const LIGHT: Palette = {
  background: '#ffffff',
  card: '#ffffff',
  border: '#d3d3da',
  text: '#1c1c22',
  muted: '#6b6b76',
  edge: '#80808b',
  edgeMuted: '#b0b0ba',
  success: '#178a44',
  failed: '#c8382f',
  running: '#5d4fe0',
  warning: '#9a5d0b',
  aborted: '#9333ea',
  neutral: '#80808b',
};

const DARK: Palette = {
  background: '#15151a',
  card: '#1f1f26',
  border: '#3b3b46',
  text: '#ececf1',
  muted: '#9a9aa6',
  edge: '#8b8b97',
  edgeMuted: '#5a5a66',
  success: '#22c55e',
  failed: '#ef5350',
  running: '#a89ff8',
  warning: '#d9a03c',
  aborted: '#c084fc',
  neutral: '#80808b',
};

/** Same grouping as `statusStrokeClass`. */
function statusColor(status: NodeStatus, palette: Palette): string {
  switch (status) {
    case NodeStatus.Success:
      return palette.success;
    case NodeStatus.Running:
      return palette.running;
    case NodeStatus.Retrying:
    case NodeStatus.Waiting:
    case NodeStatus.PartialSuccess:
      return palette.warning;
    case NodeStatus.Failed:
    case NodeStatus.Rejected:
      return palette.failed;
    case NodeStatus.Aborted:
      return palette.aborted;
    default:
      return palette.neutral;
  }
}

/** Escapes text for XML content and attribute values. Drops characters that XML forbids. */
function esc(text: string): string {
  return (
    text
      // eslint-disable-next-line no-control-regex
      .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f￾￿]/g, '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&apos;')
  );
}

/** Rounds to two decimals so that the output is short and stable. */
function num(value: number): string {
  return String(Math.round(value * 100) / 100);
}

/** Cuts the text to fit `maxPx` with a character-count estimate, and adds an ellipsis. */
function truncate(text: string, maxPx: number, charPx: number): string {
  const chars = Array.from(text);
  const max = Math.max(1, Math.floor(maxPx / charPx));
  if (chars.length <= max) {
    return text;
  }
  return `${chars.slice(0, max - 1).join('')}…`;
}

interface Point {
  x: number;
  y: number;
}

function isRouter(node: CanvasNode): boolean {
  return node.kind === 'router' || node.action === 'router.route';
}

interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

function boxOf(
  node: CanvasNode,
  positions: NodePositions,
  compact: boolean,
  direction: CanvasDirection
): (Box & { node: CanvasNode }) | null {
  const p = positions[node.key];
  if (!p) {
    return null;
  }
  return { node, x: p.x, y: p.y, ...nodeSize(node, compact, direction) };
}

function nodeStroke(node: CanvasNode, palette: Palette): string {
  return node.run ? statusColor(node.run.status, palette) : palette.border;
}

function text(
  x: number,
  y: number,
  content: string,
  attrs: {
    size: number;
    fill: string;
    family: string;
    weight?: string;
    anchor?: string;
  }
): string {
  const weight = attrs.weight ? ` font-weight="${attrs.weight}"` : '';
  const anchor = attrs.anchor ? ` text-anchor="${attrs.anchor}"` : '';
  return `<text x="${num(x)}" y="${num(y)}" font-family="${esc(attrs.family)}" font-size="${attrs.size}"${weight}${anchor} fill="${attrs.fill}">${esc(content)}</text>`;
}

/** Initials of a step, the stand-in of its icon in a compact card. */
function initials(label: string): string {
  return Array.from(label.trim()).slice(0, 2).join('').toUpperCase();
}

function drawCard(
  full: Box & { node: CanvasNode },
  palette: Palette,
  compactMode: boolean,
  direction: CanvasDirection
): string {
  const { node } = full;
  const compact = compactMode && hasCompactCaption(node);
  // A compact step is a square in a box that also holds its caption.
  const offset = compact ? compactCardOffset(direction) : { left: 0, top: 0 };
  const box = compact
    ? { ...full, width: COMPACT_NODE_SIZE, height: COMPACT_NODE_SIZE }
    : full;
  const subDag = node.kind === 'call';
  const stacked = subDag && node.parallel === true;
  const handler = node.lane === 'handler';
  const stroke = nodeStroke(node, palette);
  const dash = handler ? ' stroke-dasharray="4 3"' : '';
  const rect = (dx: number, fill: string, color: string, dashed: string) =>
    `<rect x="${num(dx)}" y="${num(dx)}" width="${box.width}" height="${box.height}" rx="6" ry="6" fill="${fill}" stroke="${color}" stroke-width="1"${dashed}/>`;
  const parts: string[] = [];
  if (stacked) {
    parts.push(rect(GHOST_STEP * 2, palette.card, palette.border, ''));
    parts.push(rect(GHOST_STEP, palette.card, palette.border, ''));
  }
  parts.push(rect(0, palette.card, stroke, dash));
  if (subDag) {
    for (const x of [8, box.width - 8]) {
      parts.push(
        `<line x1="${x}" y1="0" x2="${x}" y2="${box.height}" stroke="${palette.border}" stroke-width="1"/>`
      );
    }
  }
  if (compact) {
    // The card is a square with the initials in place of the icon.
    parts.push(
      text(box.width / 2, box.height / 2 + 5, initials(node.label), {
        size: 14,
        weight: '700',
        fill: palette.text,
        family: SANS,
        anchor: 'middle',
      })
    );
    if (node.run) {
      parts.push(
        `<circle cx="${box.width - 8}" cy="8" r="3.5" fill="${stroke}"/>`
      );
    }
  } else {
    const left = subDag ? 20 : 14;
    const room = box.width - left - (subDag ? 20 : 14) - (node.run ? 14 : 0);
    const subtitle = node.subtitle ?? (subDag ? node.subDag : undefined);
    const labelY = subtitle ? 28 : 37;
    parts.push(
      text(
        left,
        labelY,
        truncate(node.label, Math.min(room, 190), LABEL_CHAR_PX),
        {
          size: 13,
          weight: '700',
          fill: palette.text,
          family: SANS,
        }
      )
    );
    if (subtitle) {
      parts.push(
        text(left, 47, truncate(subtitle, room, MONO_CHAR_PX), {
          size: 11,
          fill: palette.muted,
          family: MONO,
        })
      );
    }
    if (node.run) {
      parts.push(
        `<circle cx="${box.width - (subDag ? 20 : 14)}" cy="${labelY - 4}" r="4" fill="${stroke}"/>`
      );
    }
  }
  if (handler) {
    const caption = 'Handler';
    parts.push(
      `<rect x="8" y="${-HANDLER_CAPTION_RISE}" width="${num(caption.length * 6 + 10)}" height="14" rx="3" ry="3" fill="${palette.card}" stroke="${palette.border}" stroke-width="1"/>`,
      text(13, 2, caption, {
        size: 9,
        weight: '600',
        fill: palette.muted,
        family: SANS,
      })
    );
  }
  const shape = stacked ? 'stack' : subDag ? 'subroutine' : 'card';
  const card = compact
    ? `<g transform="translate(${num(offset.left)} ${num(offset.top)})">${parts.join('')}</g>`
    : parts.join('');
  // The caption of a compact card: the name, or the id, always drawn.
  const caption = compact
    ? text(
        full.width / 2,
        offset.top + COMPACT_NODE_SIZE + COMPACT_CAPTION_HEIGHT - 6,
        truncate(node.label, COMPACT_CAPTION_WIDTH, CAPTION_CHAR_PX),
        {
          size: 12,
          fill: palette.text,
          family: SANS,
          anchor: 'middle',
        }
      )
    : '';
  return `<g data-node="${esc(node.key)}" data-shape="${shape}" transform="translate(${num(full.x)} ${num(full.y)})">${card}${caption}</g>`;
}

function drawDiamond(
  box: Box & { node: CanvasNode },
  palette: Palette,
  direction: CanvasDirection,
  routesWithEdge: Set<string>
): string {
  const { node, height, width: w } = box;
  const stroke = nodeStroke(node, palette);
  const parts: string[] = [
    `<polygon points="${w / 2},0 ${w},${num(height / 2)} ${w / 2},${num(height)} 0,${num(height / 2)}" fill="${palette.card}" stroke="${stroke}" stroke-width="1" stroke-linejoin="round"/>`,
  ];
  const value = node.routeValue ?? node.subtitle;
  const labelY = value ? height / 2 - 2 : height / 2 + 4;
  parts.push(
    text(w / 2, labelY, truncate(node.label, 112, LABEL_CHAR_PX), {
      size: 13,
      weight: '700',
      fill: palette.text,
      family: SANS,
      anchor: 'middle',
    })
  );
  if (value) {
    parts.push(
      text(w / 2, labelY + 14, truncate(value, 112, SMALL_MONO_CHAR_PX), {
        size: 10,
        fill: palette.muted,
        family: MONO,
        anchor: 'middle',
      })
    );
  }
  if (node.run) {
    parts.push(
      `<circle cx="${w / 2}" cy="${num(height / 2 - 20)}" r="3.5" fill="${stroke}"/>`
    );
  }
  const routes = node.routes ?? [];
  const ports = routePorts(routes.length, w, direction);
  const lr = direction === 'LR';
  routes.forEach((pattern, index) => {
    if (routesWithEdge.has(pattern)) {
      return;
    }
    const port = ports[index] ?? { x: w, y: height / 2 };
    parts.push(
      text(
        port.x + (lr ? 8 : 0),
        port.y + (lr ? -4 : 16),
        truncate(pattern, 96, SMALL_MONO_CHAR_PX),
        {
          size: 10,
          fill: palette.muted,
          family: MONO,
          anchor: lr ? 'start' : 'middle',
        }
      )
    );
  });
  return `<g data-node="${esc(node.key)}" data-shape="diamond" transform="translate(${num(box.x)} ${num(box.y)})">${parts.join('')}</g>`;
}

interface EdgeStyle {
  dash: string;
  color: string;
  marker: string;
  cap: string;
}

function edgeStyle(kind: CanvasEdge['kind'], palette: Palette): EdgeStyle {
  switch (kind) {
    case 'inferred':
      return {
        dash: ' stroke-dasharray="6 4"',
        color: palette.edge,
        marker: 've-arrow',
        cap: '',
      };
    case 'order':
    case 'agent':
      return {
        dash: ' stroke-dasharray="2 4"',
        color: palette.edgeMuted,
        marker: 've-arrow-muted',
        cap: ' stroke-linecap="round"',
      };
    default:
      return { dash: '', color: palette.edge, marker: 've-arrow', cap: '' };
  }
}

/** The markup of an edge and the points that bound its curve and label. */
function drawEdge(
  edge: CanvasEdge,
  source: Box & { node: CanvasNode },
  target: Box & { node: CanvasNode },
  direction: CanvasDirection,
  palette: Palette,
  obstacles: readonly Obstacle[],
  waypoints: readonly Point[] | undefined
): { markup: string; extent: Point[] } {
  const lr = direction === 'LR';
  let from: Point = lr
    ? { x: source.x + source.width, y: source.y + source.height / 2 }
    : { x: source.x + source.width / 2, y: source.y + source.height };
  // A route edge leaves from the port of its pattern.
  if (
    edge.kind === 'route' &&
    isRouter(source.node) &&
    edge.label !== undefined
  ) {
    const routes = source.node.routes ?? [];
    const index = routes.indexOf(edge.label);
    if (index >= 0) {
      const port = routePorts(routes.length, source.width, direction)[index];
      if (port) {
        from = { x: source.x + port.x, y: source.y + port.y };
      }
    }
  }
  const to: Point = lr
    ? { x: target.x, y: target.y + target.height / 2 }
    : { x: target.x + target.width / 2, y: target.y };
  // The same curve as the canvas: through the waypoints of the layout, around
  // the other cards, ending on the outer edge of the entry dot.
  const segments = routedCurve(
    {
      sourceX: from.x,
      sourceY: from.y,
      sourcePosition: lr ? Position.Right : Position.Bottom,
      targetX: to.x,
      targetY: to.y,
      targetPosition: lr ? Position.Left : Position.Top,
      waypoints: waypoints ?? [],
    },
    obstacles
  );
  const path = curvePathData(segments);
  const extent: Point[] = sampleCurve(segments);
  const { x: labelX, y: labelY } = curvePointAt(segments, 0.5);
  const style = edgeStyle(edge.kind, palette);
  let out = `<path data-edge="${esc(edge.id)}" data-kind="${edge.kind}" d="${path.replace(/-?\d+\.\d+/g, (m) => num(Number(m)))}" fill="none" stroke="${style.color}" stroke-width="1.5"${style.dash}${style.cap} marker-end="url(#${style.marker})"/>`;
  if (edge.kind === 'route' && edge.label) {
    const label = truncate(edge.label, 150, SMALL_MONO_CHAR_PX);
    const width = Array.from(label).length * SMALL_MONO_CHAR_PX + 8;
    const x = lr ? labelX - width / 2 : labelX + 8;
    const y = lr ? labelY - 22 : labelY - 8;
    extent.push({ x, y }, { x: x + width, y: y + 16 });
    out += `<g data-edge-label="${esc(edge.id)}"><rect x="${num(x)}" y="${num(y)}" width="${num(width)}" height="16" rx="3" ry="3" fill="${palette.card}" stroke="${palette.border}" stroke-width="1"/>${text(x + width / 2, y + 12, label, { size: 10, fill: palette.muted, family: MONO, anchor: 'middle' })}</g>`;
  }
  return { markup: out, extent };
}

function markers(palette: Palette): string {
  const arrow = (id: string, color: string) =>
    `<marker id="${id}" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="9" markerHeight="9" markerUnits="userSpaceOnUse" orient="auto"><path d="M0,0 L10,5 L0,10 z" fill="${color}"/></marker>`;
  return `<defs>${arrow('ve-arrow', palette.edge)}${arrow('ve-arrow-muted', palette.edgeMuted)}</defs>`;
}

/**
 * Standalone SVG of the graph, drawn from the node positions. The SVG has
 * literal colors and no external resources, so it renders the same in a
 * browser, an editor or an image. `width` and `height` include a margin.
 */
export function buildExportSvg(
  graph: CanvasGraph,
  positions: NodePositions,
  direction: CanvasDirection,
  opts: {
    dark?: boolean;
    compact?: boolean;
    /** The points that the long edges cross, by edge id, from the layout. */
    waypoints?: Record<string, Point[]>;
  } = {}
): { svg: string; width: number; height: number } {
  const palette = opts.dark ? DARK : LIGHT;
  const compact = opts.compact ?? false;
  const boxes = new Map<string, Box & { node: CanvasNode }>();
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const node of graph.nodes) {
    const box = boxOf(node, positions, compact, direction);
    if (!box) {
      continue;
    }
    boxes.set(node.key, box);
    const ghost = node.kind === 'call' && node.parallel ? GHOST_STEP * 2 : 0;
    const rise = node.lane === 'handler' ? HANDLER_CAPTION_RISE : 0;
    // The caption of a compact card can hang out of its box.
    const captioned = compact && hasCompactCaption(node);
    const hang = captioned ? (COMPACT_CAPTION_WIDTH - box.width) / 2 : 0;
    const below = captioned
      ? compactCardOffset(direction).top +
        COMPACT_NODE_SIZE +
        COMPACT_CAPTION_HEIGHT -
        box.height
      : 0;
    minX = Math.min(minX, box.x - Math.max(0, hang));
    minY = Math.min(minY, box.y - rise);
    maxX = Math.max(maxX, box.x + box.width + ghost + Math.max(0, hang));
    maxY = Math.max(maxY, box.y + box.height + ghost + Math.max(0, below));
  }
  if (boxes.size === 0) {
    minX = minY = maxX = maxY = 0;
  }

  const routesWithEdge = new Map<string, Set<string>>();
  for (const edge of graph.edges) {
    if (edge.kind === 'route' && edge.label !== undefined) {
      const set = routesWithEdge.get(edge.source) ?? new Set<string>();
      set.add(edge.label);
      routesWithEdge.set(edge.source, set);
    }
  }

  // A detour around a card and a route label can reach past the cards.
  const edges: string[] = [];
  for (const edge of graph.edges) {
    const source = boxes.get(edge.source);
    const target = boxes.get(edge.target);
    // The tools hang from the agent without a line, as on the canvas.
    if (
      !source ||
      !target ||
      edge.source === edge.target ||
      edge.kind === 'agent'
    ) {
      continue;
    }
    const obstacles = [...boxes.entries()]
      .filter(([key]) => key !== edge.source && key !== edge.target)
      .map(([, box]) => box);
    const drawn = drawEdge(
      edge,
      source,
      target,
      direction,
      palette,
      obstacles,
      opts.waypoints?.[edge.id]
    );
    edges.push(drawn.markup);
    for (const point of drawn.extent) {
      minX = Math.min(minX, point.x);
      minY = Math.min(minY, point.y);
      maxX = Math.max(maxX, point.x);
      maxY = Math.max(maxY, point.y);
    }
  }
  const width = Math.round(maxX - minX + 2 * MARGIN);
  const height = Math.round(maxY - minY + 2 * MARGIN);

  const nodes: string[] = [];
  for (const node of graph.nodes) {
    const box = boxes.get(node.key);
    if (!box) {
      continue;
    }
    nodes.push(
      isRouter(node)
        ? drawDiamond(
            box,
            palette,
            direction,
            routesWithEdge.get(node.key) ?? new Set()
          )
        : drawCard(box, palette, compact, direction)
    );
  }

  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">` +
    markers(palette) +
    `<rect x="0" y="0" width="${width}" height="${height}" fill="${palette.background}"/>` +
    `<g transform="translate(${num(MARGIN - minX)} ${num(MARGIN - minY)})">` +
    `<g data-layer="edges">${edges.join('')}</g>` +
    `<g data-layer="nodes">${nodes.join('')}</g>` +
    `</g></svg>`;
  return { svg, width, height };
}

/** Saves the blob as a file through a temporary link. */
function saveBlob(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = fileName;
  link.style.display = 'none';
  document.body.appendChild(link);
  link.click();
  link.remove();
  // The download has started when the click returns; revoke after the task.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

/** Downloads the SVG as `${baseName}.svg`. */
export function downloadSvg(svg: string, baseName: string): void {
  saveBlob(
    new Blob([svg], { type: 'image/svg+xml;charset=utf-8' }),
    `${baseName}.svg`
  );
}

/**
 * Rasterizes the SVG and downloads it as `${baseName}.png`. `width` and
 * `height` are the size of the SVG; the PNG is `scale` times larger.
 * Rejects when the browser cannot load or draw the image.
 */
export async function downloadPng(
  svg: string,
  width: number,
  height: number,
  baseName: string,
  scale = 2
): Promise<void> {
  const image = await new Promise<HTMLImageElement>((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error('Cannot load the SVG as an image'));
    // A data URL keeps the canvas clean in every browser.
    img.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  });
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(width * scale));
  canvas.height = Math.max(1, Math.round(height * scale));
  const context = canvas.getContext('2d');
  if (!context) {
    throw new Error('Canvas 2D is not available');
  }
  context.drawImage(image, 0, 0, canvas.width, canvas.height);
  const blob = await new Promise<Blob | null>((resolve) =>
    canvas.toBlob(resolve, 'image/png')
  );
  if (!blob) {
    throw new Error('Cannot encode the PNG');
  }
  saveBlob(blob, `${baseName}.png`);
}
