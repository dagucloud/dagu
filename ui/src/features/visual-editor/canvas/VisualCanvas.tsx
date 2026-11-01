// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useEffect,
  useId,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { useResolvedTheme } from '@/contexts/UserPreference';
import { useI18n } from '@/i18n/I18nProvider';
import { readClipboardText } from '@/lib/clipboard';
import { cn } from '@/lib/utils';
import { frozenMessage } from '../editMessages';
import type {
  CanvasDirection,
  CanvasViewHandle,
  DagEditOp,
  PaletteTarget,
  StepCommand,
  StepKey,
  VisualCanvasProps,
} from '../contracts';
import {
  CanvasAffordancesContext,
  type CanvasAffordances,
} from './affordances';
import { CanvasToolbar } from './CanvasToolbar';
import { useCanvasOverlayInset } from './overlayInset';
import {
  TOOLBAR_GROUP_CLASS,
  useCanvasToolbarHost,
  useToolbarHostWidth,
} from './toolbarHost';
import { revealDelta } from './revealDelta';
import { SimplifiedToggle } from './SimplifiedToggle';
import { ContextMenu } from './ContextMenu';
import {
  contextMenuItems,
  type ContextMenuGates,
  type ContextMenuRow,
  type ContextMenuRowId,
} from './contextMenuItems';
import { ExportMenu } from './ExportMenu';
import { CanvasActionsContext, type CanvasActions } from './context';
import { edgeTypes as stepEdgeTypes } from './edges';
import { EmptyCanvasTile } from './EmptyCanvasTile';
import {
  Background,
  Controls,
  Flow,
  FlowProvider,
  MiniMap,
  Panel,
  useFlow,
  type Connection,
  type ConnectionEndState,
} from './engine';
import type { MenuPoint } from './engine/Flow';
import { boundsOf, clamp, snapZoom, zoomAt } from './engine/geometry';
import { MINIMAP_RESERVE } from './engine/overlays';
import type { Viewport } from './engine/types';
import { useFlowStore } from './engine/store';
import { buildExportSvg, downloadPng, downloadSvg } from './exportCanvas';
import {
  ghostFlowEdges,
  launcherFlowEdges,
  toFlowEdges,
  withBundles,
  withWaypoints,
  toFlowNodes,
  toolMatches,
  withGhostLook,
  withLauncherStates,
} from './flow';
import { GHOST_KEY, isGhostKey, planGhost } from './ghost';
import {
  canInsertOnEdge,
  connectionToOp,
  droppedConnectionSource,
  edgeDeleteOp,
  edgeLockReason,
  isValidConnection,
  refusedConnection,
  selectionDeleteOps,
  stepDeleteOp,
} from './gestures';
import { KeyHelpDialog } from './KeyHelpDialog';
import { agentColumns, isAgentGraph } from './layout';
import { agentNeighbour, neighbourStep, type NavDirection } from './neighbours';
import { RenameStepPopover } from './RenameStepPopover';
import { ToolFilter } from './ToolFilter';
import { AGENT_NODE_KEY, type CanvasNodeWithRoutes } from './types';
import { nodeTypes as stepNodeTypes } from './nodes';
import { GhostNode } from './nodes/GhostNode';
import { GhostEdge } from './edges/GhostEdge';
import { LauncherEdge } from '../launchers/LauncherEdge';
import {
  LauncherActionsContext,
  LauncherNode,
  type LauncherActions,
} from '../launchers/LauncherNode';
import {
  isLauncherKey,
  launcherIdOf,
  launcherKey,
  withLaunchers,
} from '../launchers/model';
import { canvasShortcut, useCanvasKeymap } from './useCanvasKeymap';
import { useAutoFit } from './useAutoFit';
import { useCanvasDetails } from './useCanvasDetails';
import { useCanvasLayout } from './useCanvasLayout';

// The floating toolbar is 56 px high: a fit keeps its content 16 px below it
// and 24 px from the sides, so no node sits under an overlay. The bottom
// keeps the overview and the list toggle clear.
const FIT_INSET = { top: 72, right: 24, left: 24, bottom: 48 };
// The toolbar stacked on two rows (a small free width) needs more room.
const STACKED_TOP = 112;
// A toolbar in the row of the zone floats over the top 40 px of the canvas: a
// fit keeps its content 8 px below it.
const HOSTED_TOP = 48;
// A graph is not shrunk below this zoom by the automatic fit: it shows the
// start of the graph at a readable size, and the Fit button gives the
// overview.
const READABLE_ZOOM = 0.5;
// An agent graph is read as a whole: its hub and tool cards keep a larger
// minimum, and the Fit button stops there too.
const AGENT_READABLE_ZOOM = 0.75;
const AGENT_READABLE_FLOOR = 0.5;
// The Fit button of a large classic graph stops at this zoom, so that a long
// workflow never turns into a stripe; the zoom out gesture goes lower.
const OVERVIEW_READABLE_ZOOM = 0.25;
const OVERVIEW_MIN_NODES = 30;
// A reveal that a layout change (a dock that opens) follows is applied again
// for this long, to the new size of the canvas.
const REVEAL_SETTLE_MS = 1000;
// Space kept free around a node that a keyboard move brings into view.
const REVEAL_MARGIN = 12;
// The ghost of the Add step list is brought into view after the layout.
const GHOST_REVEAL_DELAY_MS = 80;
// A graph of 100 steps in a row needs less than the default 10 %. The zoom
// steps are round percentages, from 5 % to 400 %.
const MIN_ZOOM = 0.02;
const MAX_ZOOM = 4;
// The toolbar shows icons only below this free width, and stacks below the
// second one; the overview needs room too.
const COMPACT_WIDTH = 960;
const STACKED_WIDTH = 520;
// The toolbars in the row of the zone show icons only below this row width.
const HOSTED_COMPACT_WIDTH = 1200;
const MINIMAP_MIN_WIDTH = 480;

const NO_LAUNCHERS: readonly never[] = [];
const ZOOM_SHORTCUTS = {
  zoomIn: canvasShortcut('zoomIn'),
  zoomOut: canvasShortcut('zoomOut'),
  fit: canvasShortcut('fit'),
};
const nodeTypes = {
  ...stepNodeTypes,
  launcher: LauncherNode,
  ghost: GhostNode,
};
const edgeTypes = {
  ...stepEdgeTypes,
  launcher: LauncherEdge,
  ghost: GhostEdge,
};

type MenuState =
  | { kind: 'node'; key: StepKey; point: MenuPoint }
  | { kind: 'edge'; id: string; point: MenuPoint }
  | { kind: 'pane'; point: MenuPoint };

/** The node element of a step, found without a CSS escape of the key. */
function nodeElement(
  root: HTMLElement | null,
  key: StepKey
): HTMLElement | null {
  if (!root) {
    return null;
  }
  return (
    Array.from(root.querySelectorAll<HTMLElement>('[data-flow-node]')).find(
      (element) => element.dataset.flowNode === key
    ) ?? null
  );
}

function CanvasContexts({
  actions,
  affordances,
  children,
}: {
  actions: CanvasActions;
  affordances: CanvasAffordances | null;
  children: ReactNode;
}): ReactElement {
  return (
    <CanvasActionsContext.Provider value={actions}>
      <CanvasAffordancesContext.Provider value={affordances}>
        {children}
      </CanvasAffordancesContext.Provider>
    </CanvasActionsContext.Provider>
  );
}

/**
 * Interactive DAG canvas. It shows `graph` and turns user gestures into
 * `onEdit` operations and `onCommand` commands. It never edits YAML text and
 * keeps node positions only in component state.
 */
export function VisualCanvas(props: VisualCanvasProps): ReactElement {
  return (
    <FlowProvider minZoom={MIN_ZOOM} maxZoom={MAX_ZOOM}>
      <CanvasView {...props} />
    </FlowProvider>
  );
}

function CanvasView({
  graph,
  direction,
  selected,
  frozen,
  unparsedLine,
  readOnly,
  hideReadOnlyChip = false,
  onSelect,
  onEdit,
  onCommand,
  onDirectionChange,
  toolbarEnd,
  exportName = 'workflow',
  highlighted = null,
  onAddStep,
  ghost = null,
  viewRef,
  hideFrozenNotice = false,
  stepCommandGates,
  canPaste = false,
  escapeToHost = false,
  onRefused,
  launchers = NO_LAUNCHERS,
  activeLauncher = null,
  onLauncher,
}: VisualCanvasProps): ReactElement {
  const { ts } = useI18n();
  const wrapperRef = useRef<HTMLDivElement>(null);
  const hintId = useId();
  const editable = !frozen && !readOnly;

  const agent = isAgentGraph(graph);
  // The columns of the tool grid follow the width of the canvas.
  const canvasWidth = useFlowStore().size.width;
  // A panel over the canvas covers its right: the free part is what shows.
  const overlayRight = useCanvasOverlayInset().right;
  const freeWidth = canvasWidth - overlayRight;
  // Cards without text: only their icon, so that a graph is denser.
  const [showDetails, setShowDetails] = useCanvasDetails();
  const compact = !showDetails;
  // The toolbars sit in the row of the zone when the host gives one. The row
  // has the width of the zone, so no panel changes their form.
  const toolbarHost = useCanvasToolbarHost();
  const hosted = toolbarHost !== undefined;
  const hostWidth = useToolbarHostWidth(toolbarHost);
  const compactBar = hosted
    ? hostWidth !== null && hostWidth < HOSTED_COMPACT_WIDTH
    : canvasWidth > 0 && freeWidth < COMPACT_WIDTH;
  const stackedBar = !hosted && canvasWidth > 0 && freeWidth < STACKED_WIDTH;
  // A large graph keeps its overview on: the fit leaves its place free.
  const large = graph.nodes.length > OVERVIEW_MIN_NODES;
  const minimapShown = freeWidth >= MINIMAP_MIN_WIDTH;
  const fitInset = useMemo(
    () => ({
      ...FIT_INSET,
      left: FIT_INSET.left + (large && minimapShown ? MINIMAP_RESERVE : 0),
      right: FIT_INSET.right + overlayRight,
      top: hosted ? HOSTED_TOP : stackedBar ? STACKED_TOP : FIT_INSET.top,
    }),
    [overlayRight, stackedBar, hosted, large, minimapShown]
  );
  // The automatic fit opens a large graph at a readable zoom. The Fit button
  // shows all of a small graph, and stops at a readable zoom for a large one.
  const readableFrom =
    direction === 'LR' && !agent ? ('left' as const) : ('top' as const);
  const overviewOptions = useMemo(
    () => ({
      padding: 0.2,
      maxZoom: 1.25,
      inset: fitInset,
      readableFrom,
      ...(agent
        ? {
            readableZoom: AGENT_READABLE_ZOOM,
            readableFloor: AGENT_READABLE_FLOOR,
          }
        : large
          ? { readableZoom: OVERVIEW_READABLE_ZOOM, overview: true }
          : {}),
    }),
    [fitInset, agent, large, readableFrom]
  );
  const fitOptions = useMemo(
    () => ({
      ...overviewOptions,
      readableZoom: agent ? AGENT_READABLE_ZOOM : READABLE_ZOOM,
      overview: false,
      ...(agent ? { readableFloor: AGENT_READABLE_FLOOR } : {}),
    }),
    [overviewOptions, agent]
  );
  const columns = agent ? agentColumns(canvasWidth) : undefined;
  // The launchers are roots of the layout and of the drawing, never of the
  // graph: the edits, the keys and the export read `graph`.
  // The ghost of the Add step list is a node of the layout too, so that the
  // other steps make room for it. It is computed only while the list is open.
  const ghostTarget = editable ? (ghost?.target ?? null) : null;
  const ghostLook = ghost?.look ?? null;
  const ghostPlan = useMemo(
    () =>
      ghostTarget === null ? null : planGhost(graph, selected, ghostTarget),
    [graph, selected, ghostTarget]
  );
  const launched = useMemo(
    () => withLaunchers(ghostPlan?.graph ?? graph, launchers),
    [graph, ghostPlan, launchers]
  );
  const { positions, waypoints, bundles, moveNodes, realign } = useCanvasLayout(
    launched.graph,
    direction,
    columns,
    compact
  );

  const [toolFilter, setToolFilter] = useState('');
  const baseNodes = useMemo(
    () =>
      withLauncherStates(
        toFlowNodes(
          launched.graph,
          positions,
          selected,
          highlighted,
          toolFilter,
          compact,
          direction
        ),
        launchers,
        activeLauncher ? launcherKey(activeLauncher) : null
      ),
    [
      launched,
      positions,
      selected,
      highlighted,
      toolFilter,
      compact,
      direction,
      launchers,
      activeLauncher,
    ]
  );
  // A hover in the list changes the look of the ghost only, not the layout.
  const nodes = useMemo(
    () => (ghostPlan ? withGhostLook(baseNodes, ghostLook) : baseNodes),
    [baseNodes, ghostPlan, ghostLook]
  );
  const launcherActions = useMemo<LauncherActions>(
    () => ({
      open: (id) => onLauncher?.(id),
      active: activeLauncher,
    }),
    [onLauncher, activeLauncher]
  );
  const toolCount = useMemo(() => {
    const needle = toolFilter.trim().toLowerCase();
    const tools = graph.nodes.filter(
      (node) => (node as CanvasNodeWithRoutes).tool
    );
    return {
      total: tools.length,
      matches: tools.filter((node) => toolMatches(node, needle)).length,
    };
  }, [graph, toolFilter]);
  const matchingTools = useMemo(() => {
    const needle = toolFilter.trim().toLowerCase();
    return needle === ''
      ? []
      : graph.nodes
          .filter(
            (node) =>
              (node as CanvasNodeWithRoutes).tool && toolMatches(node, needle)
          )
          .map((node) => node.key);
  }, [graph, toolFilter]);
  const edges = useMemo(() => {
    const hidden = new Set(ghostPlan?.hidden);
    return withBundles(
      withWaypoints(
        [
          ...toFlowEdges(graph, { editable, selectedEdgeId: null }).filter(
            (edge) => !hidden.has(edge.id)
          ),
          ...launcherFlowEdges(launched, launchers),
          ...ghostFlowEdges(ghostPlan),
        ],
        waypoints
      ),
      bundles
    );
  }, [graph, editable, launched, launchers, ghostPlan, waypoints, bundles]);

  const selectStep = useCallback(
    (step: StepKey | null) => {
      onSelect(step);
    },
    [onSelect]
  );

  // Every edit goes through this guard: a frozen or read-only canvas never
  // calls onEdit.
  const emit = useCallback(
    (op: DagEditOp | null) => {
      if (op && editable) {
        onEdit(op);
      }
    },
    [editable, onEdit]
  );

  const onConnect = useCallback(
    (conn: Connection) => emit(connectionToOp(graph, conn)),
    [emit, graph]
  );
  const checkConnection = useCallback(
    (conn: Pick<Connection, 'source' | 'target'>) =>
      editable && isValidConnection(graph, conn),
    [editable, graph]
  );

  // The host shows the list of steps. Without a host, nothing opens.
  const openPalette = useCallback(
    (target: PaletteTarget) => {
      if (editable) {
        onAddStep?.(target);
      }
    },
    [editable, onAddStep]
  );

  const deleteEdge = useCallback(
    (edgeId: string) => {
      const edge = graph.edges.find((candidate) => candidate.id === edgeId);
      if (edge) {
        emit(edgeDeleteOp(graph, edge));
      }
    },
    [graph, emit]
  );
  // Writes again the explicit link behind an inferred edge.
  const restoreEdge = useCallback(
    (edgeId: string) => {
      const edge = graph.edges.find((candidate) => candidate.id === edgeId);
      if (edge?.kind === 'inferred') {
        emit(
          connectionToOp(graph, { source: edge.source, target: edge.target })
        );
      }
    },
    [graph, emit]
  );
  // The "+" stalks, the edge trash and the add tile exist only while the
  // canvas can edit.
  const affordances = useMemo<CanvasAffordances | null>(
    () => (editable ? { openPalette, deleteEdge, restoreEdge } : null),
    [editable, openPalette, deleteEdge, restoreEdge]
  );

  const onConnectEnd = useCallback(
    (end: ConnectionEndState) => {
      const source = droppedConnectionSource(graph, end);
      if (source) {
        openPalette({ mode: 'child', source });
        return;
      }
      const refused = refusedConnection(graph, end);
      if (refused) onRefused?.(refused);
    },
    [graph, openPalette, onRefused]
  );

  const { fitView, setViewport, zoomIn, zoomOut } = useFlow();
  // The store changes with each size and handle change; the view commands
  // read its latest value.
  const store = useFlowStore();
  const storeRef = useRef(store);
  storeRef.current = store;
  const insetRef = useRef(fitInset);
  insetRef.current = fitInset;
  const selectedRef = useRef(selected);
  selectedRef.current = selected;

  // The view that shows a node in the free area of the canvas (the part that
  // no toolbar and no panel covers). `ensure` moves the view only when the
  // node is not fully in that area, and by the least distance. Null when the
  // view stays, or when the canvas has no size or no such node yet.
  // `group` are other nodes that the view shows with the node: the centre of
  // all of them is centred, and a group wider than the area starts at its
  // left and top edges, so that no card of it is cut on that side.
  const viewForNode = useCallback(
    (
      key: StepKey,
      minZoom: number,
      ensure: boolean,
      group: readonly StepKey[] = []
    ): Viewport | null => {
      const { nodes, size, zoomLimits, getViewport } = storeRef.current;
      const node = nodes().find((candidate) => candidate.id === key);
      if (!node || size.width === 0 || size.height === 0) {
        return null;
      }
      const inset = insetRef.current;
      const area = {
        left: inset.left,
        top: inset.top,
        right: Math.max(size.width - inset.right, inset.left + 1),
        bottom: Math.max(size.height - inset.bottom, inset.top + 1),
      };
      const view = getViewport();
      const zoom = clamp(
        Math.max(view.zoom, minZoom),
        zoomLimits.min,
        zoomLimits.max
      );
      if (ensure && zoom === view.zoom) {
        const move = (
          start: number,
          length: number,
          from: number,
          to: number
        ): number => {
          const lo = from + REVEAL_MARGIN;
          const hi = to - REVEAL_MARGIN;
          if (length > hi - lo || start < lo) {
            return lo - start;
          }
          return start + length > hi ? hi - (start + length) : 0;
        };
        const dx = move(
          node.position.x * zoom + view.x,
          node.width * zoom,
          area.left,
          area.right
        );
        const dy = move(
          node.position.y * zoom + view.y,
          node.height * zoom,
          area.top,
          area.bottom
        );
        return dx === 0 && dy === 0
          ? null
          : { zoom, x: view.x + dx, y: view.y + dy };
      }
      const shown = boundsOf(
        nodes()
          .filter(
            (candidate) => candidate.id === key || group.includes(candidate.id)
          )
          .map((candidate) => ({
            x: candidate.position.x,
            y: candidate.position.y,
            width: candidate.width,
            height: candidate.height,
          }))
      ) ?? {
        x: node.position.x,
        y: node.position.y,
        width: node.width,
        height: node.height,
      };
      const place = (
        start: number,
        length: number,
        from: number,
        to: number
      ): number =>
        length * zoom > to - from - 2 * REVEAL_MARGIN && group.length > 0
          ? from + REVEAL_MARGIN - start * zoom
          : (from + to) / 2 - (start + length / 2) * zoom;
      return {
        zoom,
        x: place(shown.x, shown.width, area.left, area.right),
        y: place(shown.y, shown.height, area.top, area.bottom),
      };
    },
    []
  );
  // The view that a reveal of a step from outside the canvas needs (the Runs
  // dock, a chip, a link, the step view). The zoom stays. Null when the card
  // already sits in the central zone of the free area, when the canvas has no
  // size yet, or when there is no such node.
  const viewForZone = useCallback((key: StepKey): Viewport | null => {
    const { nodes, size, getViewport } = storeRef.current;
    const node = nodes().find((candidate) => candidate.id === key);
    if (!node || size.width === 0 || size.height === 0) {
      return null;
    }
    const view = getViewport();
    const { dx, dy } = revealDelta(
      {
        x: node.position.x * view.zoom + view.x,
        y: node.position.y * view.zoom + view.y,
        width: node.width * view.zoom,
        height: node.height * view.zoom,
      },
      { x: 0, y: 0, width: size.width, height: size.height },
      insetRef.current
    );
    return dx === 0 && dy === 0
      ? null
      : { zoom: view.zoom, x: view.x + dx, y: view.y + dy };
  }, []);
  // Moves the view only as far as needed to show the selected step.
  const keepSelectedInView = () => {
    const key = selectedRef.current;
    const view = key ? viewForNode(key, 0, true) : null;
    if (view) {
      setViewport(view);
    }
  };
  const autoFit = useAutoFit(
    wrapperRef,
    () => {
      fitView(fitOptions);
      // A resize that fits the graph again keeps the selected step in view.
      keepSelectedInView();
    },
    undefined,
    // A dock that opens changes the height only: the graph stays in place and
    // moves only when it would hide the selected step.
    keepSelectedInView
  );
  const {
    onUserMove: stopAutoFit,
    onHostMove,
    resume,
    refit,
    moved: userMoved,
  } = autoFit;
  // A reveal that waits for the size of the canvas or for a layout change.
  const pendingReveal = useRef<{
    key: StepKey;
    minZoom: number;
    group: readonly StepKey[];
    until: number;
    /** Reveal by the central-zone rule: no move when the card is there. */
    zone?: boolean;
  } | null>(null);
  const onUserMove = useCallback(() => {
    pendingReveal.current = null;
    stopAutoFit();
  }, [stopAutoFit]);
  const zoomTo = useCallback(
    (level: number) => {
      const { size, zoomLimits, getViewport } = storeRef.current;
      const inset = insetRef.current;
      onUserMove();
      setViewport(
        zoomAt(
          getViewport(),
          // A level between two steps lands on the nearest step.
          snapZoom(level, zoomLimits),
          // The centre of the free area, so that no node lands under the
          // toolbar.
          {
            x: (inset.left + size.width - inset.right) / 2,
            y: (inset.top + size.height - inset.bottom) / 2,
          },
          zoomLimits
        )
      );
    },
    [onUserMove, setViewport]
  );
  const fitAll = useCallback(() => {
    resume();
    fitView(overviewOptions);
  }, [resume, fitView, overviewOptions]);
  // Centres a node at a readable zoom. The host chose this view: a resize
  // does not fit it away.
  const showNode = useCallback(
    (
      key: StepKey,
      minZoom: number,
      ensure = false,
      group: readonly StepKey[] = []
    ) => {
      onHostMove();
      const view = viewForNode(key, minZoom, ensure, group);
      if (view) {
        setViewport(view);
      }
      if (!ensure) {
        // The canvas may not have its final size yet: the dock opens, or the
        // canvas was hidden. The effect below applies the reveal again.
        pendingReveal.current = {
          key,
          minZoom,
          group,
          until: Date.now() + REVEAL_SETTLE_MS,
        };
      }
    },
    [onHostMove, viewForNode, setViewport]
  );
  // Shows a step that was selected from outside the canvas: the view moves
  // only when the card is not in the central zone of the free area, and by
  // the least distance that puts it there. A card that is off screen is
  // centred. The zoom stays. A host move stops the fit on a resize only when
  // the view moved.
  const revealInZone = useCallback(
    (key: StepKey, settle = true) => {
      const view = viewForZone(key);
      if (view) {
        onHostMove();
        setViewport(view);
      }
      if (settle) {
        // The canvas may not have its final size yet: the dock opens, or the
        // canvas was hidden. The effect below applies the rule again.
        pendingReveal.current = {
          key,
          minZoom: 0,
          group: [],
          until: Date.now() + REVEAL_SETTLE_MS,
          zone: true,
        };
      }
    },
    [onHostMove, viewForZone, setViewport]
  );
  const flowWidth = store.size.width;
  const flowHeight = store.size.height;
  useEffect(() => {
    const pending = pendingReveal.current;
    if (!pending || Date.now() > pending.until) {
      pendingReveal.current = null;
      return;
    }
    if (pending.zone) {
      const zoned = viewForZone(pending.key);
      if (zoned) {
        onHostMove();
        setViewport(zoned);
      }
      return;
    }
    const view = viewForNode(
      pending.key,
      pending.minZoom,
      false,
      pending.group
    );
    if (view) {
      setViewport(view);
    }
  }, [
    flowWidth,
    flowHeight,
    viewForNode,
    viewForZone,
    onHostMove,
    setViewport,
  ]);

  // The ghost of the Add step list shows where the step goes: the view moves
  // only when the ghost is not fully in the free area.
  const ghostAt = ghostPlan ? positions[GHOST_KEY] : undefined;
  const ghostX = ghostAt?.x;
  const ghostY = ghostAt?.y;
  useEffect(() => {
    if (ghostX === undefined) return;
    // The flow reads the new node after this render.
    const timer = setTimeout(() => {
      const view = viewForNode(GHOST_KEY, 0, true);
      if (view) setViewport(view);
    }, GHOST_REVEAL_DELAY_MS);
    return () => clearTimeout(timer);
  }, [ghostX, ghostY, viewForNode, setViewport]);

  // The view fits again when the graph changes (undo, redo, discard, a new
  // step), when the direction changes and when a panel covers or frees the
  // right of the canvas; a view that the user panned stays, but a new
  // direction is a new layout.
  const nodeSet = graph.nodes.map((node) => node.key).join('\u0000');
  const lastLayout = useRef({ nodeSet, direction, overlayRight, compact });
  // The view that the user chose in each layout (details or compact), so that
  // a switch and the switch back leave the view where it was. It is dropped
  // when the steps or the direction change: the stored view would not fit.
  const layoutViews = useRef<Partial<Record<'details' | 'compact', Viewport>>>(
    {}
  );
  useEffect(() => {
    const last = lastLayout.current;
    lastLayout.current = { nodeSet, direction, overlayRight, compact };
    if (last.direction !== direction) {
      layoutViews.current = {};
      resume();
      fitView(fitOptions);
    } else if (last.nodeSet !== nodeSet) {
      layoutViews.current = {};
      refit();
    } else if (last.compact !== compact) {
      const views = layoutViews.current;
      if (userMoved()) {
        views[last.compact ? 'compact' : 'details'] =
          storeRef.current.getViewport();
      }
      const saved = views[compact ? 'compact' : 'details'];
      if (saved) {
        setViewport(saved);
        stopAutoFit();
      } else {
        resume();
        fitView(fitOptions);
      }
    } else if (last.overlayRight !== overlayRight) {
      refit();
    }
  }, [
    nodeSet,
    direction,
    overlayRight,
    compact,
    resume,
    refit,
    userMoved,
    stopAutoFit,
    setViewport,
    fitView,
    fitOptions,
  ]);
  // A click on the direction that is already active aligns the steps again:
  // the moves of the user are dropped, and the view fits the new layout.
  const [realigned, setRealigned] = useState(0);
  const seenRealign = useRef(0);
  useEffect(() => {
    if (realigned === seenRealign.current) {
      return;
    }
    seenRealign.current = realigned;
    resume();
    fitView(fitOptions);
  }, [realigned, resume, fitView, fitOptions]);
  const changeDirection = useCallback(
    (next: CanvasDirection) => {
      if (next === direction) {
        realign();
        setRealigned((count) => count + 1);
      } else {
        onDirectionChange?.(next);
      }
    },
    [direction, realign, onDirectionChange]
  );
  useImperativeHandle(
    viewRef,
    (): CanvasViewHandle => ({
      fitView: fitAll,
      revealStep: (key) => revealInZone(key),
      zoomTo,
      getZoom: () => storeRef.current.getViewport().zoom,
    }),
    [fitAll, revealInZone, zoomTo]
  );

  // A filter that matches tools brings the first match into view, and Enter
  // goes on to the next one.
  const matchIndex = useRef(0);
  const firstMatch = matchingTools[0];
  const matchesRef = useRef(matchingTools);
  matchesRef.current = matchingTools;
  useEffect(() => {
    matchIndex.current = 0;
    if (firstMatch !== undefined) {
      showNode(firstMatch, READABLE_ZOOM, false, matchesRef.current);
    }
    // Only a change of the text moves the view, not a change of the graph.
  }, [toolFilter, showNode]);
  const stepMatch = useCallback(
    (delta: 1 | -1) => {
      if (matchingTools.length === 0) {
        return;
      }
      matchIndex.current =
        (matchIndex.current + delta + matchingTools.length) %
        matchingTools.length;
      const key = matchingTools[matchIndex.current];
      if (key !== undefined) {
        showNode(key, READABLE_ZOOM);
      }
    },
    [matchingTools, showNode]
  );

  const [menu, setMenu] = useState<MenuState | null>(null);
  const [renaming, setRenaming] = useState<StepKey | null>(null);
  const [helpOpen, setHelpOpen] = useState(false);

  const startRename = useCallback(
    (step: StepKey) => {
      if (
        editable &&
        step !== AGENT_NODE_KEY &&
        graph.nodes.some((node) => node.key === step)
      ) {
        setMenu(null);
        setRenaming(step);
      }
    },
    [editable, graph]
  );
  const endRename = useCallback(() => {
    setRenaming(null);
    wrapperRef.current?.focus();
  }, []);

  // The keyboard opens the menu of the selection, under its node.
  const openSelectionMenu = () => {
    const rect = wrapperRef.current?.getBoundingClientRect();
    const center = {
      clientX: (rect?.left ?? 0) + (rect?.width ?? 0) / 2,
      clientY: (rect?.top ?? 0) + (rect?.height ?? 0) / 2,
    };
    const element = selected ? nodeElement(wrapperRef.current, selected) : null;
    if (selected && element) {
      const box = element.getBoundingClientRect();
      setMenu({
        kind: 'node',
        key: selected,
        point: { clientX: box.left, clientY: box.bottom },
      });
    } else {
      setMenu({ kind: 'pane', point: center });
    }
  };

  const navigate = (dir: NavDirection) => {
    const next = agent
      ? agentNeighbour(graph, selected, dir, columns ?? 1)
      : neighbourStep(graph, selected, dir, direction);
    if (next) {
      selectStep(next);
      // The selection follows the keys: the view goes to it when it is out
      // of the central zone of the free area.
      revealInZone(next, false);
    }
  };

  useCanvasKeymap(wrapperRef, {
    delete: () => {
      const ops = selectionDeleteOps(graph, {
        nodes: selected && selected !== AGENT_NODE_KEY ? [selected] : [],
        edgeIds: [],
      });
      ops.forEach(emit);
    },
    open: () => {
      if (selected) {
        onCommand({ kind: 'openInspector', step: selected });
      }
    },
    // Without a handler the key is not consumed and reaches the host.
    clear: escapeToHost ? undefined : () => selectStep(null),
    palette: () => openPalette({ mode: 'after' }),
    fit: fitAll,
    menu: openSelectionMenu,
    rename: () => {
      if (selected) {
        startRename(selected);
      }
    },
    help: () => setHelpOpen(true),
    copy: () => onCommand({ kind: 'copy' }),
    paste: (text) => {
      if (editable) {
        onCommand({ kind: 'paste', text });
      }
    },
    duplicate: () => {
      if (editable) {
        onCommand({ kind: 'duplicate' });
      }
    },
    zoomIn: () => {
      storeRef.current.noteUserMove();
      onUserMove();
      zoomIn();
    },
    zoomOut: () => {
      storeRef.current.noteUserMove();
      onUserMove();
      zoomOut();
    },
    zoomReset: () => zoomTo(1),
    navUp: () => navigate('up'),
    navDown: () => navigate('down'),
    navLeft: () => navigate('left'),
    navRight: () => navigate('right'),
  });

  const actions = useMemo<CanvasActions>(
    () => ({
      direction,
      editable,
      readOnly,
      compact,
      onCommand,
      onDeleteStep: (step) => emit(stepDeleteOp(step)),
      onInsertOnEdge: (edgeId) => openPalette({ mode: 'insert', edgeId }),
      stepCommandGates,
      clipboard: { canPaste: editable && canPaste },
      onRename: editable ? startRename : undefined,
    }),
    [
      direction,
      editable,
      readOnly,
      compact,
      onCommand,
      emit,
      openPalette,
      stepCommandGates,
      canPaste,
      startRename,
    ]
  );

  const dark = useResolvedTheme() === 'dark';
  const menuGates: ContextMenuGates = {
    editable,
    stepCommandGates,
    clipboard: { canPaste: editable && canPaste },
    direction,
    canChangeDirection: !!onDirectionChange,
  };
  const menuNode =
    menu?.kind === 'node'
      ? graph.nodes.find((node) => node.key === menu.key)
      : undefined;
  const menuEdge =
    menu?.kind === 'edge'
      ? graph.edges.find((edge) => edge.id === menu.id)
      : undefined;
  let menuRows: ContextMenuRow[] = [];
  if (menu?.kind === 'node' && menuNode) {
    menuRows = contextMenuItems('node', menuNode, menuGates);
  } else if (menu?.kind === 'edge' && menuEdge) {
    menuRows = contextMenuItems(
      'edge',
      {
        edge: menuEdge,
        insertable: canInsertOnEdge(graph, menuEdge),
        lockReason:
          edgeDeleteOp(graph, menuEdge) === null
            ? (edgeLockReason(graph, menuEdge) ?? 'Not available')
            : null,
      },
      menuGates
    );
  } else if (menu?.kind === 'pane') {
    menuRows = contextMenuItems('pane', null, menuGates);
  }
  const showSetStatus =
    !!stepCommandGates &&
    graph.nodes.some(
      (node) =>
        node.lane !== 'handler' &&
        node.serverName !== undefined &&
        stepCommandGates.canSetStatus(node.serverName)
    );

  const closeMenu = useCallback((restoreFocus: boolean) => {
    setMenu(null);
    if (restoreFocus) {
      wrapperRef.current?.focus();
    }
  }, []);

  const exportAs = (format: 'svg' | 'png') => {
    const { svg, width, height } = buildExportSvg(graph, positions, direction, {
      dark,
      compact,
      waypoints,
    });
    if (format === 'svg') {
      downloadSvg(svg, exportName);
    } else {
      void downloadPng(svg, width, height, exportName).catch(() => {});
    }
  };

  const selectRow = (id: ContextMenuRowId) => {
    const current = menu;
    closeMenu(id !== 'keyHelp');
    if (!current) {
      return;
    }
    const step = current.kind === 'node' ? current.key : null;
    const name = menuNode?.serverName;
    const stepCommand = (cmd: StepCommand) => onCommand({ kind: 'step', cmd });
    switch (id) {
      case 'viewLogs':
        if (name) stepCommand({ kind: 'viewLogs', step: name });
        return;
      case 'openSubRun':
        if (name) stepCommand({ kind: 'openSubRun', step: name });
        return;
      case 'retryStep':
        if (name) stepCommand({ kind: 'retryStep', step: name });
        return;
      case 'setStatus':
        if (name) stepCommand({ kind: 'setStatus', step: name });
        return;
      case 'addStep':
        openPalette(step ? { mode: 'child', source: step } : { mode: 'after' });
        return;
      case 'paste':
        if (editable && canPaste) {
          void readClipboardText().then((text) =>
            onCommand({ kind: 'paste', text })
          );
        }
        return;
      case 'insertStep':
        if (current.kind === 'edge') {
          openPalette({ mode: 'insert', edgeId: current.id });
        }
        return;
      case 'removeDependency':
        if (menuEdge) emit(edgeDeleteOp(graph, menuEdge));
        return;
      case 'fitView':
        fitAll();
        return;
      case 'directionLR':
        changeDirection('LR');
        return;
      case 'directionTB':
        changeDirection('TB');
        return;
      case 'exportSvg':
        exportAs('svg');
        return;
      case 'exportPng':
        exportAs('png');
        return;
      case 'keyHelp':
        setHelpOpen(true);
        return;
    }
  };

  const renamingNode = renaming
    ? graph.nodes.find((node) => node.key === renaming)
    : undefined;

  // An icon-only button keeps its name as a tooltip.
  const toolbarRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!compactBar) {
      return;
    }
    toolbarRef.current
      ?.querySelectorAll<HTMLButtonElement>('button:not([title])')
      .forEach((button) => {
        const name = button.getAttribute('aria-label') ?? button.textContent;
        if (name?.trim()) {
          button.title = name.trim();
        }
      });
  });

  const leftToolbar = (
    <CanvasToolbar
      direction={direction}
      onDirectionChange={onDirectionChange ? changeDirection : undefined}
      readOnly={readOnly && !hideReadOnlyChip}
      floating={hosted}
      trailing={
        <>
          <Controls
            fitOptions={overviewOptions}
            onFitView={autoFit.resume}
            onUserMove={onUserMove}
            shortcuts={ZOOM_SHORTCUTS}
          />
          <span className="mx-0.5 h-5 w-px bg-border" aria-hidden="true" />
          <ExportMenu
            graph={graph}
            positions={positions}
            direction={direction}
            name={exportName}
            compact={compact}
            waypoints={waypoints}
          />
          <SimplifiedToggle
            simplified={!showDetails}
            onSimplifiedChange={(simplified) => setShowDetails(!simplified)}
          />
        </>
      }
    />
  );
  const rightToolbar = (
    <div
      data-testid="ve-toolbar"
      data-compact={compactBar || undefined}
      ref={toolbarRef}
      className={cn(
        // The same padding as the left toolbar: both are 34 px high,
        // and the buttons sit on one line. A separator that starts
        // the bar has nothing to divide.
        // In the row of a host the bar has no background: each button floats.
        'flex items-center justify-end gap-1 empty:hidden [&>[aria-hidden=true]:first-child]:hidden',
        !hosted && 'rounded-md border bg-card/90 p-0.5 shadow-sm',
        // One row: the labels shrink to icons, and the rest scrolls.
        // A select keeps its text, and an overflow starts at the
        // left edge, so that every control can be reached.
        compactBar
          ? 'flex-nowrap justify-end-safe overflow-x-auto [&_button:has(>svg):not([role=combobox])]:gap-0 [&_button:has(>svg):not([role=combobox])]:text-[0px] [&>*]:shrink-0'
          : hosted
            ? 'min-w-0 flex-nowrap [&>*]:shrink-0'
            : 'flex-wrap'
      )}
    >
      {agent && toolCount.total > 0 ? (
        <ToolFilter
          value={toolFilter}
          onChange={setToolFilter}
          matches={toolCount.matches}
          total={toolCount.total}
          compact={compactBar}
          onStep={stepMatch}
        />
      ) : null}
      {toolbarEnd}
    </div>
  );

  return (
    <CanvasContexts actions={actions} affordances={affordances}>
      <div
        ref={wrapperRef}
        role="region"
        aria-label={ts('Workflow canvas')}
        aria-describedby={hintId}
        tabIndex={0}
        data-testid="ve-canvas"
        className="relative flex h-full min-h-0 w-full flex-col overflow-clip outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        onContextMenu={(event) => {
          // The menu key also sends a contextmenu event to the focused canvas.
          if (event.target === event.currentTarget) {
            event.preventDefault();
          }
        }}
      >
        <span id={hintId} className="sr-only">
          {ts('Arrow keys select a step, Enter opens it')}
        </span>
        {frozen && !hideFrozenNotice ? (
          <NoticeLine
            className="p-1"
            notices={[
              {
                id: 'frozen',
                tone: 'warning',
                priority: 0,
                message: frozenMessage(ts, unparsedLine),
              },
            ]}
          />
        ) : null}
        <div
          className={cn(
            'relative min-h-0 flex-1',
            frozen && 'opacity-60 grayscale'
          )}
          data-frozen={frozen || undefined}
        >
          <LauncherActionsContext.Provider value={launcherActions}>
            <Flow
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodesMove={moveNodes}
              onNodeClick={(key) => {
                const launcher = launcherIdOf(key);
                if (launcher) onLauncher?.(launcher);
                else if (!isGhostKey(key)) selectStep(key);
              }}
              onNodeDoubleClick={(id) => {
                if (!isLauncherKey(id) && !isGhostKey(id)) {
                  onCommand({ kind: 'openInspector', step: id });
                }
              }}
              onPaneClick={() => selectStep(null)}
              onNodeContextMenu={(key, point) => {
                if (!isLauncherKey(key) && !isGhostKey(key)) {
                  setMenu({ kind: 'node', key, point });
                }
              }}
              // A canvas that cannot edit has no edge row: keep the browser menu.
              onEdgeContextMenu={
                editable
                  ? (id, point) => setMenu({ kind: 'edge', id, point })
                  : undefined
              }
              onPaneContextMenu={(point) => setMenu({ kind: 'pane', point })}
              onUserMove={onUserMove}
              nodesDraggable={!frozen}
              nodesConnectable={editable && graph.editableEdges}
              onConnect={onConnect}
              onConnectEnd={onConnectEnd}
              isValidConnection={checkConnection}
              fitOptions={fitOptions}
              wheel="pan"
            >
              <Background gap={16} size={1} />
              {hosted ? (
                toolbarHost &&
                createPortal(
                  <>
                    {/* The marker keeps the canvas from taking a click on a
                        toolbar for a click on the pane. */}
                    <div
                      data-flow-overlay=""
                      className={cn('shrink-0', TOOLBAR_GROUP_CLASS)}
                    >
                      {leftToolbar}
                    </div>
                    <div
                      data-flow-overlay=""
                      className={cn(
                        'flex min-w-0 justify-end',
                        TOOLBAR_GROUP_CLASS
                      )}
                    >
                      {rightToolbar}
                    </div>
                  </>,
                  toolbarHost
                )
              ) : (
                <>
                  <Panel position="top-left">{leftToolbar}</Panel>
                  <Panel
                    position="top-right"
                    className={cn(stackedBar && 'top-[3.25rem]')}
                    style={{
                      // Clear of a panel over the canvas; the left toolbar keeps
                      // its room unless the bar is stacked.
                      right: overlayRight + 8,
                      maxWidth: stackedBar
                        ? `calc(100% - ${overlayRight + 16}px)`
                        : `calc(100% - ${overlayRight}px - 20rem)`,
                    }}
                  >
                    {rightToolbar}
                  </Panel>
                </>
              )}
              <MiniMap
                onUserMove={onUserMove}
                hidden={freeWidth < MINIMAP_MIN_WIDTH}
                hideWhenFitted={readOnly}
                persistent={large}
              />
            </Flow>
          </LauncherActionsContext.Provider>
          {graph.nodes.length === 0 && !ghostPlan ? <EmptyCanvasTile /> : null}
        </div>
        {menu && menuRows.length > 0 ? (
          <ContextMenu
            rows={menuRows}
            x={menu.point.clientX}
            y={menu.point.clientY}
            label={ts('Canvas menu')}
            onSelect={selectRow}
            onClose={closeMenu}
          />
        ) : null}
        {renaming && renamingNode && editable ? (
          <RenameStepPopover
            key={renaming}
            anchor={nodeElement(wrapperRef.current, renaming)}
            name={renamingNode.serverName ?? renamingNode.label}
            onSubmit={(newName) => {
              emit({ kind: 'renameStep', step: renaming, newName });
              endRename();
            }}
            onCancel={endRename}
          />
        ) : null}
        <KeyHelpDialog
          open={helpOpen}
          onOpenChange={(open) => {
            setHelpOpen(open);
            if (!open) {
              wrapperRef.current?.focus();
            }
          }}
          showSetStatus={showSetStatus}
        />
      </div>
    </CanvasContexts>
  );
}
