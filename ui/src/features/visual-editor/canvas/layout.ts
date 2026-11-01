// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  CanvasDirection,
  CanvasGraph,
  CanvasNode,
  StepKey,
} from '../contracts';
import { isLauncherKey } from '../launchers/model';
import {
  bundleGapExtra,
  detectBundles,
  MIN_BRANCH,
  reformBundle,
  trunkCross,
  trunkLength,
  type Bundle,
  type BundleCandidate,
  type EdgeBundleRef,
} from './bundles';
import type { Waypoint } from './engine/geometry';
import { isGhostKey } from './ghost';
import { layeredRoutes, type LayeredBand, type LayeredRoute } from './layered';
import {
  AGENT_NODE_KEY,
  type CanvasNodeWithRoutes,
  type NodePositions,
} from './types';

export const NODE_WIDTH = 240;
export const NODE_HEIGHT = 64;
/** Side of a step card that shows only its icon. */
export const COMPACT_NODE_SIZE = 56;
/**
 * The caption under a compact step card: its name, always shown. The layout
 * reserves the height of the line, and in a top-to-bottom layout the width of
 * the longest caption, so that two captions never touch.
 */
export const COMPACT_CAPTION_HEIGHT = 20;
export const COMPACT_CAPTION_WIDTH = 120;

/** Size of a launcher card: a pill that is narrower than a step. */
export const LAUNCHER_WIDTH = 176;
export const LAUNCHER_HEIGHT = 56;
const ROUTER_MIN_SIZE = 160;

const NODE_SEP = 32;
// Left to right, a link between two ranks is 96 px long, so that its row of
// buttons fits on it without covering a node or the arrowhead. Top to bottom,
// the buttons lie side by side across the link, so that a shorter link of 72
// px (64 px with compact cards) still shows its line and its arrowhead. The
// routing keeps its bends wide enough down to this gap: a shorter gap bends
// the curves of dense graphs below the minimum radius of the routing harness.
export const RANK_SEP_LR = 96;
export const RANK_SEP_TB = 72;
export const COMPACT_RANK_SEP_LR = 96;
export const COMPACT_RANK_SEP_TB = 64;
const COMPACT_NODE_SEP = 24;
const COMPONENT_GAP = 48;
const LANE_GAP = 96;

/** Size of a tool card of an agent DAG, and the gap between two cards. */
export const TOOL_WIDTH = 190;
export const TOOL_HEIGHT = 52;
export const TOOL_GAP = 10;
/** The hub is as wide as three tool cards. */
export const HUB_WIDTH = 3 * TOOL_WIDTH + 2 * TOOL_GAP;
const HUB_GAP = 32;
const HUB_HEAD_HEIGHT = 92;
const HUB_TASKS_HEAD_HEIGHT = 26;
const HUB_TASK_ROW_HEIGHT = 20;
/** The hub lists this many tasks, in two columns, and counts the rest. */
export const HUB_MAX_TASKS = 12;

export const AGENT_MIN_COLUMNS = 4;
export const AGENT_MAX_COLUMNS = 8;
export const AGENT_DEFAULT_COLUMNS = 6;
/** Space kept at each side of the grid when the columns are chosen. */
const GRID_MARGIN = 48;

/**
 * Columns of the tool grid that fit a canvas of `width` px at full zoom,
 * from 4 to 8. An unknown width (0) gives the default.
 */
export function agentColumns(width: number): number {
  if (!(width > 0)) {
    return AGENT_DEFAULT_COLUMNS;
  }
  const fit = Math.floor(
    (width - 2 * GRID_MARGIN + TOOL_GAP) / (TOOL_WIDTH + TOOL_GAP)
  );
  return Math.min(AGENT_MAX_COLUMNS, Math.max(AGENT_MIN_COLUMNS, fit));
}

/** Height of the hub: its head, and the rows of the tasks it lists. */
export function hubHeight(taskCount: number): number {
  const shown = Math.min(taskCount, HUB_MAX_TASKS);
  if (shown === 0) {
    return HUB_HEAD_HEIGHT;
  }
  const rows = Math.ceil(shown / 2) + (taskCount > shown ? 1 : 0);
  return HUB_HEAD_HEIGHT + HUB_TASKS_HEAD_HEIGHT + rows * HUB_TASK_ROW_HEIGHT;
}

/**
 * Side of the square box of a router. The router is a square turned by 45
 * degrees, so its diagonal is this size. It grows with its route ports.
 */
export function routerSize(routeCount: number): number {
  return Math.max(ROUTER_MIN_SIZE, 64 + 32 * routeCount);
}

/**
 * Places the route ports of a router of `size` on the edges of its diamond
 * that face the way the graph flows: the right edges in LR, the lower edges
 * in TB. The ports are spread evenly, and an odd count leaves the middle
 * free, so that the default output at the vertex has no port over it.
 */
export function routePorts(
  count: number,
  size: number,
  direction: CanvasDirection
): { x: number; y: number }[] {
  const slots = count % 2 === 0 ? count : count + 1;
  return Array.from({ length: count }, (_, index) => {
    const t = (index + 1) / (slots + 1);
    const along = size * t;
    // Distance from the middle: the edge of the diamond is that far in.
    const inset = Math.abs(2 * along - size) / 2;
    return direction === 'LR'
      ? { x: size - inset, y: along }
      : { x: along, y: size - inset };
  });
}

/**
 * True for the nodes that have a caption under their compact card: the steps
 * and the handlers. The routers, the launchers, the ghost and the cards of an
 * agent do not.
 */
export function hasCompactCaption(node: CanvasNode): boolean {
  return (
    !isGhostKey(node.key) &&
    !isLauncherKey(node.key) &&
    node.kind !== 'agent' &&
    !(node as CanvasNodeWithRoutes).tool &&
    node.kind !== 'router' &&
    node.action !== 'router.route'
  );
}

/**
 * The box of a compact step with its caption. The card is centred on the
 * cross axis, so that the handles on the middle of the box sides stay on the
 * middle of the card. Top to bottom, the caption is under the card inside the
 * box, and the link leaves under the caption. Left to right, the box has the
 * height of the caption on top of the card, and the caption hangs out of its
 * lower side by half of that.
 */
export function compactBox(direction: CanvasDirection): {
  width: number;
  height: number;
} {
  return {
    width: direction === 'TB' ? COMPACT_CAPTION_WIDTH : COMPACT_NODE_SIZE,
    height: COMPACT_NODE_SIZE + COMPACT_CAPTION_HEIGHT,
  };
}

/** Top-left corner of the card in its compact box, in px. */
export function compactCardOffset(direction: CanvasDirection): {
  left: number;
  top: number;
} {
  const box = compactBox(direction);
  return {
    left: (box.width - COMPACT_NODE_SIZE) / 2,
    top: direction === 'TB' ? 0 : (box.height - COMPACT_NODE_SIZE) / 2,
  };
}

/**
 * Size of a node card. A router is a square box and grows with its ports.
 * With `compact`, a step or a handler is a square of `COMPACT_NODE_SIZE` with
 * its caption under it (see `compactBox`); the agent hub, the tool cards and
 * the routers keep their size.
 */
export function nodeSize(
  node: CanvasNode,
  compact = false,
  direction: CanvasDirection = 'TB'
): { width: number; height: number } {
  // The ghost is a plain card, whatever action it shows.
  if (isGhostKey(node.key)) {
    return compact
      ? { width: COMPACT_NODE_SIZE, height: COMPACT_NODE_SIZE }
      : { width: NODE_WIDTH, height: NODE_HEIGHT };
  }
  if (isLauncherKey(node.key)) {
    return compact
      ? { width: COMPACT_NODE_SIZE, height: COMPACT_NODE_SIZE }
      : { width: LAUNCHER_WIDTH, height: LAUNCHER_HEIGHT };
  }
  if (node.kind === 'agent') {
    const tasks = (node as CanvasNodeWithRoutes).hub?.tasks.length ?? 0;
    return { width: HUB_WIDTH, height: hubHeight(tasks) };
  }
  if ((node as CanvasNodeWithRoutes).tool) {
    return { width: TOOL_WIDTH, height: TOOL_HEIGHT };
  }
  const isRouter = node.kind === 'router' || node.action === 'router.route';
  if (isRouter) {
    const size = routerSize(node.routes?.length ?? 0);
    return { width: size, height: size };
  }
  if (compact) {
    return compactBox(direction);
  }
  return { width: NODE_WIDTH, height: NODE_HEIGHT };
}

/**
 * Connected components of the main lane. Edges of every kind count, and
 * their direction does not. Keys keep the YAML order inside a component, and
 * components follow the YAML index of their first node.
 */
export function connectedComponents(graph: CanvasGraph): StepKey[][] {
  const main = graph.nodes.filter((node) => node.lane === 'main');
  const index = new Map<StepKey, number>();
  main.forEach((node, i) => index.set(node.key, i));
  const parent = main.map((_, i) => i);
  const find = (i: number): number => {
    let root = i;
    while (parent[root] !== root) {
      root = parent[root] ?? root;
    }
    while (parent[i] !== root) {
      const next = parent[i] ?? root;
      parent[i] = root;
      i = next;
    }
    return root;
  };
  for (const edge of graph.edges) {
    const a = index.get(edge.source);
    const b = index.get(edge.target);
    if (a === undefined || b === undefined) {
      continue;
    }
    const rootA = find(a);
    const rootB = find(b);
    // The smaller root wins, so that a root is the first node of its component.
    parent[Math.max(rootA, rootB)] = Math.min(rootA, rootB);
  }
  const components = new Map<number, StepKey[]>();
  main.forEach((node, i) => {
    const root = find(i);
    const list = components.get(root);
    if (list) {
      list.push(node.key);
    } else {
      components.set(root, [node.key]);
    }
  });
  return [...components.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([, keys]) => keys);
}

/** No prototype: a step named `__proto__` keeps a key of its own. */
function emptyPositions(): NodePositions {
  return Object.create(null);
}

interface ComponentLayout {
  positions: NodePositions;
  bundles: Bundle[];
  /** Length of the trunk of each bundle, in px. */
  trunk: number;
  routes: LayeredRoute[];
  bands: Record<string, LayeredBand>;
  width: number;
  height: number;
}

/**
 * How an edge can join a bundle. The edges of a launcher and the plain
 * dependencies do. A route, an inferred link, an order link and an edge of the
 * ghost step keep their own line: each one means something of its own.
 */
function candidateOf(edge: CanvasGraph['edges'][number]): BundleCandidate {
  const ghost = isGhostKey(edge.source) || isGhostKey(edge.target);
  const kind = ghost
    ? null
    : isLauncherKey(edge.source)
      ? ('launcher' as const)
      : edge.kind === 'depends'
        ? ('depends' as const)
        : null;
  return { id: edge.id, source: edge.source, target: edge.target, kind };
}

/** Gap in px between two ranks, before a bundle asks for more. */
function rankSepOf(direction: CanvasDirection, compact: boolean): number {
  return direction === 'LR'
    ? compact
      ? COMPACT_RANK_SEP_LR
      : RANK_SEP_LR
    : compact
      ? COMPACT_RANK_SEP_TB
      : RANK_SEP_TB;
}

/**
 * Lay out one component in ranks. The keys come in YAML order, so siblings
 * follow the YAML order. The result starts at the origin.
 */
function layoutComponent(
  graph: CanvasGraph,
  keys: StepKey[],
  direction: CanvasDirection,
  compact: boolean
): ComponentLayout {
  const byKey = new Map(graph.nodes.map((node) => [node.key, node]));
  const nodes = keys.flatMap((key) => {
    const node = byKey.get(key);
    return node ? [{ key, ...nodeSize(node, compact, direction) }] : [];
  });
  const members = new Set(keys);
  const edges = graph.edges.filter(
    (edge) =>
      edge.source !== edge.target &&
      members.has(edge.source) &&
      members.has(edge.target)
  );
  const rankSep = rankSepOf(direction, compact);
  const input = {
    nodes,
    edges,
    direction,
    nodeSep: compact ? COMPACT_NODE_SEP : NODE_SEP,
    rankSep,
  };
  let result = layeredRoutes(input);
  // The edges that share a trunk. A gap between two ranks that holds a trunk
  // grows when the curves that merge into it need room for a smooth bend:
  // the ranks, the orders and the cross positions do not change.
  const bundles = detectBundles(
    edges.map(candidateOf),
    (key) => result.bands[key]?.rank
  );
  if (bundles.length > 0) {
    const horizontal = direction === 'LR';
    const size = new Map(nodes.map((node) => [node.key, node]));
    const cross = (key: string) => {
      const at = result.positions[key];
      const node = size.get(key);
      return !at || !node
        ? 0
        : horizontal
          ? at.y + node.height / 2
          : at.x + node.width / 2;
    };
    const extra = bundleGapExtra(
      bundles,
      cross,
      rankSep,
      Math.max(...Object.values(result.bands).map((band) => band.rank)) + 1
    );
    if (extra.some((value) => value > 0)) {
      result = layeredRoutes({ ...input, gapExtra: extra });
    }
  }
  const { positions: placed, routes, bands } = result;

  const positions = emptyPositions();
  let width = 0;
  let height = 0;
  for (const node of nodes) {
    const p = placed[node.key];
    if (!p) {
      continue;
    }
    const x = Math.round(p.x);
    const y = Math.round(p.y);
    positions[node.key] = { x, y };
    width = Math.max(width, x + node.width);
    height = Math.max(height, y + node.height);
  }
  return {
    positions,
    bundles,
    trunk: trunkLength(rankSep),
    routes,
    bands,
    width,
    height,
  };
}

/** True for the graph of an agent DAG: a hub and its tool cards. */
export function isAgentGraph(graph: CanvasGraph): boolean {
  return graph.nodes.some(
    (node) => node.key === AGENT_NODE_KEY && node.kind === 'agent'
  );
}

/**
 * Hub on top and the tools in a grid under it, row by row in the YAML order.
 * The hub is centred over the grid. The handler lane follows the grid. The
 * agent has no order of steps, so the direction does not change the result.
 */
function layoutAgent(
  graph: CanvasGraph,
  columns: number,
  compact: boolean,
  direction: CanvasDirection
): NodePositions {
  const positions = emptyPositions();
  const tools = graph.nodes.filter(
    (node) => node.lane === 'main' && node.key !== AGENT_NODE_KEY
  );
  const used = Math.max(1, Math.min(columns, tools.length));
  const gridWidth = used * (TOOL_WIDTH + TOOL_GAP) - TOOL_GAP;
  const width = Math.max(HUB_WIDTH, gridWidth);
  const hub = graph.nodes.find((node) => node.key === AGENT_NODE_KEY);
  const top = hub ? nodeSize(hub).height + HUB_GAP : 0;
  if (hub) {
    positions[hub.key] = { x: Math.round((width - HUB_WIDTH) / 2), y: 0 };
  }
  const left = Math.round((width - gridWidth) / 2);
  tools.forEach((node, i) => {
    positions[node.key] = {
      x: left + (i % columns) * (TOOL_WIDTH + TOOL_GAP),
      y: top + Math.floor(i / columns) * (TOOL_HEIGHT + TOOL_GAP),
    };
  });
  const rows = Math.ceil(tools.length / columns);
  let x = 0;
  const y =
    top +
    (rows > 0 ? rows * (TOOL_HEIGHT + TOOL_GAP) - TOOL_GAP + LANE_GAP : 0);
  for (const node of graph.nodes) {
    if (node.lane !== 'handler') {
      continue;
    }
    positions[node.key] = { x, y };
    x += nodeSize(node, compact, direction).width + NODE_SEP;
  }
  return positions;
}

/** Points that the curve of each long edge crosses, by edge id. */
export type EdgeWaypoints = Record<string, Waypoint[]>;

/** The bundle of each edge that shares a trunk, by edge id. */
export type EdgeBundles = Record<string, EdgeBundleRef>;

export interface GraphLayout {
  positions: NodePositions;
  /** Where the edges that share a trunk merge and split. */
  bundles: EdgeBundles;
  /**
   * The way of each edge that spans several ranks: the free column of each
   * rank that it crosses, so that it goes around the cards between its ends.
   */
  waypoints: EdgeWaypoints;
}

/**
 * Top-left positions of the nodes, as integers. The same graph and direction
 * always give the same positions. Components stack on the cross axis, and the
 * handler lane follows the main graph. An agent graph is a grid of `columns`
 * tool cards under its hub. With `compact`, the step cards are small squares
 * and the gaps between them shrink.
 */
export function layoutGraph(
  graph: CanvasGraph,
  direction: CanvasDirection,
  columns: number = AGENT_DEFAULT_COLUMNS,
  compact = false
): NodePositions {
  return layoutRoutes(graph, direction, columns, compact).positions;
}

/**
 * The way of each edge of a component between two ranks. An S-shaped curve
 * lies only in the space between two bands, where no card is: so an edge
 * first runs straight from its handle to the end of the band of its source,
 * crosses the free column of each band between its ends, and runs straight
 * from the start of the band of its target to its handle.
 */
function edgeWaypoints(
  graph: CanvasGraph,
  part: ComponentLayout,
  direction: CanvasDirection,
  compact: boolean,
  dx: number,
  dy: number
): { waypoints: EdgeWaypoints; bundles: EdgeBundles } {
  const horizontal = direction === 'LR';
  const byKey = new Map(graph.nodes.map((node) => [node.key, node]));
  const long = new Map(
    part.routes.map((route) => [`${route.source}\u0000${route.target}`, route])
  );
  const spot = (along: number, cross: number) =>
    horizontal ? { x: along, y: cross } : { x: cross, y: along };
  const result: EdgeWaypoints = {};
  const shared: EdgeBundles = {};
  // The trunk of each bundle: its two junctions, in the middle of the gap
  // between the band of the sources and the band of the targets.
  const junctions = new Map<
    string,
    { bundle: Bundle; merge: Waypoint; split: Waypoint }
  >();
  const centreCross = (key: string) => {
    const at = part.positions[key];
    const node = byKey.get(key);
    if (!at || !node) {
      return 0;
    }
    const size = nodeSize(node, compact, direction);
    return horizontal ? at.y + size.height / 2 : at.x + size.width / 2;
  };
  for (const bundle of part.bundles) {
    const from = part.bands[bundle.sources[0]!];
    const to = part.bands[bundle.targets[0]!];
    if (!from || !to) {
      continue;
    }
    const gap = to.start - from.end;
    const trunk = part.trunk;
    if (gap < trunk + 2 * MIN_BRANCH) {
      continue;
    }
    const cross = trunkCross(bundle, centreCross);
    const mergeAt = from.end + (gap - trunk) / 2;
    const junction = {
      bundle,
      merge: { ...spot(mergeAt, cross), hold: true },
      split: { ...spot(mergeAt + trunk, cross), hold: true },
    };
    for (const id of bundle.edges) {
      junctions.set(id, junction);
    }
  }
  for (const edge of graph.edges) {
    const source = byKey.get(edge.source);
    const target = byKey.get(edge.target);
    const from = part.bands[edge.source];
    const to = part.bands[edge.target];
    const sp = part.positions[edge.source];
    const tp = part.positions[edge.target];
    if (!source || !target || !from || !to || !sp || !tp) {
      continue;
    }
    if (to.rank <= from.rank) {
      continue;
    }
    const sourceSize = nodeSize(source, compact, direction);
    const targetSize = nodeSize(target, compact, direction);
    // The cross position of the handle that the edge leaves, and that it enters.
    let leave = horizontal
      ? sp.y + sourceSize.height / 2
      : sp.x + sourceSize.width / 2;
    let leaveAlong = horizontal
      ? sp.x + sourceSize.width
      : sp.y + sourceSize.height;
    const routes = source.routes;
    if (edge.kind === 'route' && routes && edge.label !== undefined) {
      const port = routePorts(routes.length, sourceSize.width, direction)[
        routes.indexOf(edge.label)
      ];
      if (port) {
        leave = horizontal ? sp.y + port.y : sp.x + port.x;
        leaveAlong = horizontal ? sp.x + port.x : sp.y + port.y;
      }
    }
    const enter = horizontal
      ? tp.y + targetSize.height / 2
      : tp.x + targetSize.width / 2;
    const enterAlong = horizontal ? tp.x : tp.y;
    const points: Waypoint[] = [];
    if (from.end - leaveAlong > 0.5) {
      points.push(spot(from.end, leave));
    }
    const route = long.get(`${edge.source}\u0000${edge.target}`);
    if (route) {
      points.push(...route.points);
    }
    const junction = junctions.get(edge.id);
    if (junction) {
      points.push(junction.merge, junction.split);
      const move = (p: Waypoint): Waypoint => ({
        x: p.x + dx,
        y: p.y + dy,
      });
      shared[edge.id] = {
        id: junction.bundle.id,
        kind: junction.bundle.kind,
        merge: move(junction.merge),
        split: move(junction.split),
        sources: junction.bundle.sources,
        targets: junction.bundle.targets,
      };
    }
    if (enterAlong - to.start > 0.5) {
      points.push(spot(to.start, enter));
    }
    if (points.length > 0) {
      result[edge.id] = points.map((p) => ({
        ...p,
        x: p.x + dx,
        y: p.y + dy,
      }));
    }
  }
  return { waypoints: result, bundles: shared };
}

/** The layout of `layoutGraph`, and the waypoints of the long edges. */
export function layoutRoutes(
  graph: CanvasGraph,
  direction: CanvasDirection,
  columns: number = AGENT_DEFAULT_COLUMNS,
  compact = false
): GraphLayout {
  if (isAgentGraph(graph)) {
    return {
      positions: layoutAgent(
        graph,
        Math.max(1, Math.floor(columns)),
        compact,
        direction
      ),
      waypoints: {},
      bundles: {},
    };
  }
  const horizontal = direction === 'LR';
  const positions = emptyPositions();
  const waypoints: EdgeWaypoints = {};
  const bundles: EdgeBundles = {};
  let offset = 0;
  let mainRight = 0;
  let mainBottom = 0;
  for (const keys of connectedComponents(graph)) {
    const part = layoutComponent(graph, keys, direction, compact);
    const dx = horizontal ? 0 : offset;
    const dy = horizontal ? offset : 0;
    for (const [key, p] of Object.entries(part.positions)) {
      positions[key] = { x: p.x + dx, y: p.y + dy };
    }
    const routed = edgeWaypoints(graph, part, direction, compact, dx, dy);
    Object.assign(waypoints, routed.waypoints);
    Object.assign(bundles, routed.bundles);
    mainRight = Math.max(mainRight, part.width + dx);
    mainBottom = Math.max(mainBottom, part.height + dy);
    offset += (horizontal ? part.height : part.width) + COMPONENT_GAP;
  }

  const hasMain = mainRight > 0;
  let x = horizontal && hasMain ? mainRight + LANE_GAP : 0;
  let y = !horizontal && hasMain ? mainBottom + LANE_GAP : 0;
  for (const node of graph.nodes) {
    if (node.lane !== 'handler') {
      continue;
    }
    positions[node.key] = { x, y };
    const size = nodeSize(node, compact, direction);
    if (horizontal) {
      y += size.height + NODE_SEP;
    } else {
      x += size.width + NODE_SEP;
    }
  }
  return { positions, waypoints, bundles };
}

/**
 * The waypoints and the bundles for cards that the user dragged. An edge that
 * has a dragged end loses the way that the layout gave it. A bundle that has
 * a dragged card is formed again from the current places of its cards: its
 * junctions lie between the actual sources and targets, and every edge of
 * the bundle goes through them. A bundle that the new places leave no room
 * for falls apart: its edges are routed one by one. The other bundles stay.
 */
export function reformRouting(
  graph: CanvasGraph,
  direction: CanvasDirection,
  compact: boolean,
  positions: NodePositions,
  layout: Pick<GraphLayout, 'waypoints' | 'bundles'>,
  moved: ReadonlySet<StepKey>
): Pick<GraphLayout, 'waypoints' | 'bundles'> {
  const waypoints: EdgeWaypoints = {};
  const bundles: EdgeBundles = {};
  const byKey = new Map(graph.nodes.map((node) => [node.key, node]));
  const byId = new Map(graph.edges.map((edge) => [edge.id, edge]));
  const touches = (id: string) => {
    const edge = byId.get(id);
    return !!edge && (moved.has(edge.source) || moved.has(edge.target));
  };
  const groups = new Map<string, { ref: EdgeBundleRef; ids: string[] }>();
  for (const [id, ref] of Object.entries(layout.bundles)) {
    const group = groups.get(ref.id);
    if (group) {
      group.ids.push(id);
    } else {
      groups.set(ref.id, { ref, ids: [id] });
    }
  }
  const boxOf = (key: string) => {
    const node = byKey.get(key);
    const at = positions[key];
    return node && at
      ? { ...at, ...nodeSize(node, compact, direction) }
      : undefined;
  };
  const inBundle = new Set<string>();
  for (const { ref, ids } of groups.values()) {
    ids.forEach((id) => inBundle.add(id));
    const dragged = [...ref.sources, ...ref.targets].some((key) =>
      moved.has(key)
    );
    if (!dragged) {
      for (const id of ids) {
        bundles[id] = layout.bundles[id]!;
        const way = layout.waypoints[id];
        if (way) {
          waypoints[id] = way;
        }
      }
      continue;
    }
    const reformed = reformBundle(
      {
        sources: ref.sources,
        targets: ref.targets,
        edges: ids.flatMap((id) => {
          const edge = byId.get(id);
          return edge ? [edge] : [];
        }),
      },
      boxOf,
      direction === 'LR',
      rankSepOf(direction, compact)
    );
    for (const id of ids) {
      if (reformed) {
        bundles[id] = {
          ...layout.bundles[id]!,
          merge: reformed.merge,
          split: reformed.split,
        };
        const way = reformed.waypoints[id];
        if (way) {
          waypoints[id] = way;
        }
      } else if (!touches(id)) {
        // The old junction is no longer a place of the edge.
        const way = layout.waypoints[id]?.filter((point) => !point.hold);
        if (way && way.length > 0) {
          waypoints[id] = way;
        }
      }
    }
  }
  for (const [id, way] of Object.entries(layout.waypoints)) {
    if (!inBundle.has(id) && !touches(id)) {
      waypoints[id] = way;
    }
  }
  return { waypoints, bundles };
}

/**
 * Key of everything that decides the layout: node keys, lanes, card shapes
 * and routes, edge ids, direction and the compact flag. It ignores labels,
 * issues and run state.
 */
export function graphShapeKey(
  graph: CanvasGraph,
  direction: CanvasDirection,
  columns: number = AGENT_DEFAULT_COLUMNS,
  compact = false
): string {
  const agent = isAgentGraph(graph);
  return JSON.stringify([
    direction,
    compact,
    agent ? columns : null,
    graph.nodes.map((node) => [
      node.key,
      node.lane,
      // The card shape that nodeSize picks.
      node.kind === 'agent',
      node.kind === 'router' || node.action === 'router.route',
      Boolean((node as CanvasNodeWithRoutes).tool),
      node.routes ?? null,
      // The hub grows with the tasks that it lists.
      (node as CanvasNodeWithRoutes).hub?.tasks.length ?? null,
    ]),
    graph.edges.map((edge) => edge.id),
  ]);
}
