// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus, type components } from '@/api/v1/schema';
import type {
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
  StepKey,
} from '../contracts';
import { toolNameOf, type ToolEntryModel } from '../tools/model';
import { hasEffort } from './modelEdits';
import type { PromptFit } from './promptCard';
import { promptFit } from './promptCard';
import { planToolCard, splitDagEntries } from './toolGroups';
import {
  DAG_CARD_MAX_ENTRIES,
  slotCardRows,
  type CardSlot,
  type SubItem,
  type SubSlot,
} from './subRow';
import type {
  CanvasNodeWithRoutes,
  NodePositions,
  SlotCardData,
} from './types';

type ToolDefinition = components['schemas']['ToolDefinition'];

const SATELLITE_PREFIX = 'toolsat:';

/** Key of the Model sub-node of a chat step: the round node, or the card of pickers. */
export function modelSatelliteKey(owner: StepKey): string {
  return `${SATELLITE_PREFIX}${owner}:model`;
}

/**
 * Key of the dashed "+" sub-node of the Model slot of a chat step. Only a
 * canvas with no controls (a run view, a read-only or a compact canvas) draws
 * it, while the step names no model.
 */
export function addSatelliteKey(owner: StepKey): string {
  return `${SATELLITE_PREFIX}${owner}:add-model`;
}

/** Key of the small round "+" of a Tools, Skills or DAGs slot that has no entry. */
export function addSlotKey(owner: StepKey, slot: CardSlot | 'prompt'): string {
  return `${SATELLITE_PREFIX}${owner}:add-${slot}`;
}

/** Key of the Prompt card of an agent step. */
export function promptCardKey(owner: StepKey): string {
  return `${SATELLITE_PREFIX}${owner}:prompt`;
}

/** Key of the card of the Tools, Skills or DAGs slot of a chat step. */
export function slotCardKey(owner: StepKey, slot: CardSlot): string {
  return `${SATELLITE_PREFIX}${owner}:${slot}`;
}

/** True when the node key is a sub-node of a chat step, not a step. */
export function isToolSatelliteKey(key: string): boolean {
  return key.startsWith(SATELLITE_PREFIX);
}

/** True for the layout edge from a step to one of its sub-nodes. */
export function isToolSatelliteEdge(edge: Pick<CanvasEdge, 'id'>): boolean {
  return edge.id.startsWith(SATELLITE_PREFIX);
}

/** The step key that a sub-node key belongs to, or null for any other key. */
export function satelliteOwner(key: string): StepKey | null {
  if (!isToolSatelliteKey(key)) return null;
  const rest = key.slice(SATELLITE_PREFIX.length);
  return rest.slice(0, rest.lastIndexOf(':'));
}

/**
 * True for the round sub-node of the model of a step, or for its dashed "+":
 * the nodes that open the Model section of the form of their step. The cards
 * and the "+" of the Tools and Skills slots are not: they open their own
 * section.
 */
export function isModelSubNode(node: CanvasNode): boolean {
  const sub = node as CanvasNodeWithRoutes;
  return !!(sub.modelSat || sub.addSat?.slot === 'model');
}

function subNodeBase(key: string, label: string): CanvasNodeWithRoutes {
  return {
    key,
    label,
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
  };
}

/** True for a sub-node of a chat step: the model, a slot card, the Prompt card, or a "+". */
export function isSubNode(node: CanvasNode): boolean {
  const sub = node as CanvasNodeWithRoutes;
  return !!(sub.slotCard || sub.promptCard || sub.modelSat || sub.addSat);
}

/**
 * Adds the sub-nodes of the chat steps and of the agent steps to a graph,
 * right after their step: the Model node (the card of pickers when `controls`
 * is true and the step has edit data, even while it names no model; a dashed
 * "+" while a canvas with no controls shows a step that names no model), the
 * Prompt card of an agent step (a small round "+" while it has no text), and for each of the Skills, Tools and DAGs slots either a card with a
 * row for each entry and, when `editable`, a row that adds one, or, while the
 * slot has no entry, a small round "+" that opens the dock of the slot. The
 * DAGs card holds the DAG tools of the step, each with a miniature of the DAG
 * that it calls. The Tools card of an agent step lists its MCP servers and its
 * built-in tools, as the card of a chat step lists its MCP servers. Each has one
 * edge from the connector of its slot. The rows of the Tools card go in groups
 * by kind, with a heading for each group when the step has more than one kind
 * of tool. The step gets the shape of its sub-nodes, so that the layout
 * reserves the room. The result is for the layout and the drawing only; the
 * edits read the graph without them. It is the same graph when it has no chat
 * step.
 */
export function withToolSatellites(
  graph: CanvasGraph,
  editable = true,
  controls = editable
): CanvasGraph {
  const nodes: CanvasNode[] = [];
  const edges: CanvasEdge[] = [];
  for (const node of graph.nodes as CanvasNodeWithRoutes[]) {
    if (node.lane !== 'main' || !node.chat) {
      nodes.push(node);
      continue;
    }
    const subs: {
      node: CanvasNodeWithRoutes;
      slot: SubSlot;
      rows?: number;
      cols?: number;
      empty?: boolean;
      prompt?: PromptFit;
      modelCard?: { rows: number; cli: boolean; effort: boolean };
      dagCard?: { entries: number; rows: number };
    }[] = [];
    const models = node.chat.models;
    const first = models[0];
    const edit = node.chat.edit;
    // The edit view draws the pickers even while the step names no model: the
    // user fills them, with no detour by the form. A list with no model has no
    // row to fill, so the "+" stays for it.
    const card =
      controls && edit && edit.rows.length > 0
        ? {
            rows: edit.rows.length,
            cli: edit.cli !== '',
            effort: hasEffort(edit),
          }
        : undefined;
    subs.push({
      slot: 'model',
      ...(card ? { modelCard: card } : {}),
      node:
        first || card
          ? {
              ...subNodeBase(modelSatelliteKey(node.key), first?.name ?? ''),
              modelSat: {
                owner: node.key,
                models,
                ...(edit ? { edit } : {}),
                ...(card ? { card } : {}),
              },
            }
          : {
              ...subNodeBase(addSatelliteKey(node.key), ''),
              addSat: { owner: node.key, slot: 'model' },
            },
    });
    // An agent step has a Prompt card, after the Model: its system message and prompt.
    const texts = node.chat.texts;
    if (texts) {
      const filled = texts.system.trim() !== '' || texts.prompt.trim() !== '';
      subs.push(
        filled
          ? {
              slot: 'prompt',
              prompt: promptFit(texts.system, texts.prompt),
              node: {
                ...subNodeBase(promptCardKey(node.key), 'Prompt'),
                promptCard: { owner: node.key, ...texts, editable },
              },
            }
          : {
              slot: 'prompt',
              empty: true,
              node: {
                ...subNodeBase(addSlotKey(node.key, 'prompt'), ''),
                addSat: { owner: node.key, slot: 'prompt' },
              },
            }
      );
    }
    const { dags: dagEntries, others: mcpEntries } = splitDagEntries(
      node.tools?.entries ?? []
    );
    const builtin = node.tools?.builtin ?? [];
    const skills = node.chat.skills;
    const toolCount = mcpEntries.length + builtin.length;
    const limits = node.chat.limits;
    for (const slot of ['skills', 'tools', 'dags'] as const) {
      const count =
        slot === 'tools'
          ? toolCount
          : slot === 'dags'
            ? dagEntries.length
            : skills.length;
      if (count === 0) {
        // An empty slot is a small round "+", no card.
        subs.push({
          slot,
          empty: true,
          node: {
            ...subNodeBase(addSlotKey(node.key, slot), ''),
            addSat: { owner: node.key, slot },
          },
        });
        continue;
      }
      const plan = slot === 'tools' ? planToolCard(mcpEntries, builtin) : null;
      // The limits of the tool loop sit on the Tools card; a step with DAG
      // tools only has none, and its DAGs card shows them.
      const showLimits =
        limits !== undefined &&
        (slot === 'tools' || (slot === 'dags' && toolCount === 0));
      let rows: number;
      let dagCard: { entries: number; rows: number } | undefined;
      if (slot === 'dags') {
        const more = dagEntries.length > DAG_CARD_MAX_ENTRIES ? 1 : 0;
        const extra = more + (editable ? 1 : 0) + (showLimits ? 1 : 0);
        dagCard = {
          entries: Math.min(dagEntries.length, DAG_CARD_MAX_ENTRIES),
          rows: extra,
        };
        rows = extra;
      } else {
        rows = plan
          ? plan.rows + (editable ? 1 : 0) + (showLimits ? 1 : 0)
          : slotCardRows(count, editable);
      }
      const cols = plan?.columns.length ?? 1;
      const card: SlotCardData = {
        owner: node.key,
        slot,
        entries:
          slot === 'tools' ? mcpEntries : slot === 'dags' ? dagEntries : [],
        skills: slot === 'skills' ? skills : [],
        editable,
        rows,
      };
      if (cols > 1) card.cols = cols;
      if (showLimits && limits) card.limits = limits;
      if (slot === 'tools' && builtin.length > 0) card.builtin = builtin;
      if (node.serverName !== undefined) card.stepName = node.serverName;
      if (node.run?.status === NodeStatus.Running) card.active = true;
      subs.push({
        slot,
        rows,
        cols,
        ...(dagCard ? { dagCard } : {}),
        node: {
          ...subNodeBase(
            slotCardKey(node.key, slot),
            slot === 'tools' ? 'Tools' : slot === 'dags' ? 'DAGs' : 'Skills'
          ),
          slotCard: card,
        },
      });
    }
    const items: SubItem[] = subs.map(
      ({ slot, node: sub, rows, cols, empty, prompt, modelCard, dagCard }) => ({
        key: sub.key,
        slot,
        ...(rows === undefined ? {} : { rows }),
        ...(cols && cols > 1 ? { cols } : {}),
        ...(empty ? { empty } : {}),
        ...(prompt ? { prompt } : {}),
        ...(modelCard ? { modelCard } : {}),
        ...(dagCard ? { dagCard } : {}),
      })
    );
    const owner: CanvasNodeWithRoutes = { ...node, subRow: { items } };
    nodes.push(owner, ...subs.map((sub) => sub.node));
    for (const { slot, node: sub } of subs) {
      edges.push({
        id: `${sub.key}>edge`,
        source: node.key,
        target: sub.key,
        kind: 'order',
        label: slot,
      });
    }
  }
  return nodes.length === graph.nodes.length
    ? graph
    : { ...graph, nodes, edges: [...graph.edges, ...edges] };
}

/**
 * The places of a drag. The sub-nodes of each moved chat step move with it by
 * the same distance, so they keep their offsets to it. A sub-node that the
 * drag names moves alone, to its own place: its step stays.
 */
export function withFollowers(
  graph: CanvasGraph,
  positions: NodePositions,
  moved: NodePositions
): NodePositions {
  const result: NodePositions = {};
  const followers = new Map<StepKey, string[]>();
  for (const node of graph.nodes as CanvasNodeWithRoutes[]) {
    if (node.subRow) {
      followers.set(
        node.key,
        node.subRow.items.map((item) => item.key)
      );
    }
  }
  for (const [key, to] of Object.entries(moved)) {
    if (isToolSatelliteKey(key)) continue;
    result[key] = to;
    const from = positions[key];
    if (!from) continue;
    for (const follower of followers.get(key) ?? []) {
      const at = positions[follower];
      if (at) {
        result[follower] = { x: at.x + to.x - from.x, y: at.y + to.y - from.y };
      }
    }
  }
  // A sub-node that moves alone keeps its own place, also in a drag of its step.
  for (const [key, to] of Object.entries(moved)) {
    if (isToolSatelliteKey(key)) result[key] = to;
  }
  return result;
}

/** How a run treated one tool of the catalog. */
export interface ToolMark {
  state: 'offered' | 'unknown';
}

const UNKNOWN: ToolMark = { state: 'unknown' };

function markOf(defs: ToolDefinition[]): ToolMark {
  return defs.length === 0 ? UNKNOWN : { state: 'offered' };
}

/**
 * Whether the run offered the tool of an entry. `defs` is the effective
 * catalog of the step. An entry that the catalog does not name is unknown.
 * A DAG entry also matches its tool name, for a record that has no source.
 */
export function entryMark(
  entry: ToolEntryModel,
  defs: readonly ToolDefinition[] | undefined
): ToolMark {
  if (!defs) return UNKNOWN;
  const sourceOf = (kind: string) =>
    defs.filter((def) => def.source === `${kind}:${entry.label}`);
  switch (entry.kind) {
    case 'dag': {
      const bySource = sourceOf('dag');
      if (bySource.length > 0) return markOf(bySource);
      return markOf(defs.filter((def) => def.name === toolNameOf(entry.label)));
    }
    case 'mcp':
      return markOf(sourceOf('mcp'));
    default:
      return UNKNOWN;
  }
}
