// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import type { CanvasGraph, CanvasGraphInput } from '../../contracts';
import { buildCanvasGraph } from '../graph';
import {
  cardPositions,
  chatHandleAlong,
  chatPlan,
  layoutGraph,
  layoutRoutes,
  nodeSize,
} from '../layout';
import { toFlowEdges, toFlowNodes, toolSatelliteFlowEdges } from '../flow';
import {
  addSatelliteKey,
  entryMark,
  isToolSatelliteEdge,
  isToolSatelliteKey,
  modelSatelliteKey,
  promptCardKey,
  satelliteOwner,
  slotCardKey,
  addSlotKey,
  isModelSubNode,
  withFollowers,
  withToolSatellites,
} from '../toolSatellites';
import {
  DAG_CARD_WIDTH,
  MODEL_CARD_WIDTH,
  SUB_TILE_SIZE,
  dagCardHeight,
  modelCardHeight,
} from '../subRow';
import { chatAnchors } from '../nodes/SubRowFrame';
import type { CanvasNodeWithRoutes } from '../types';
import { readStepTools } from '../../tools/parse';
import { makeModel, makeNames, makeStep } from './fixtures';

type ToolDefinition = components['schemas']['ToolDefinition'];

const CHAT = {
  action: 'chat.completion',
  with: {
    prompt: 'hi',
    dags: [{ name: 'lookup' }, { name: 'refund' }],
    tools: {
      mcp: [
        { name: 'docs', url: 'https://mcp.example.com', allow: ['search'] },
      ],
    },
  },
};

function chatGraph(): CanvasGraph {
  const model = makeModel([
    makeStep({ key: 'steps[0]', explicitName: 'fetch', value: { run: 'x' } }),
    makeStep({
      key: 'steps[1]',
      explicitName: 'triage',
      kind: 'action',
      action: 'chat.completion',
      depends: ['fetch'],
      value: CHAT,
    }),
    makeStep({
      key: 'steps[2]',
      explicitName: 'after',
      depends: ['triage'],
      value: { run: 'y' },
    }),
  ]);
  return buildCanvasGraph({
    model,
    names: makeNames(model),
    issues: { byStep: {}, dag: [] },
  });
}

// An edge of a chat step runs straight from the edge of the box to the card,
// between the columns of sub-nodes, so that it never crosses them.
describe('the edges of a chat step', () => {
  function fanGraph(): CanvasGraph {
    const chat = (key: string, name: string, depends: string[]) =>
      makeStep({
        key,
        explicitName: name,
        kind: 'action',
        action: 'chat.completion',
        depends,
        value: CHAT,
      });
    const model = makeModel([
      makeStep({ key: 'steps[0]', explicitName: 'fetch', value: { run: 'x' } }),
      chat('steps[1]', 'left', ['fetch']),
      chat('steps[2]', 'right', ['fetch']),
      makeStep({
        key: 'steps[3]',
        explicitName: 'after',
        depends: ['left', 'right'],
        value: { run: 'y' },
      }),
    ]);
    return withToolSatellites(
      buildCanvasGraph({
        model,
        names: makeNames(model),
        issues: { byStep: {}, dag: [] },
      })
    );
  }

  // The layout routes the edges to the places where the handles are drawn.
  it.each([
    ['TB', false],
    ['TB', true],
    ['LR', false],
    ['LR', true],
  ] as const)(
    'puts its waypoints on the handles of the card (%s, compact %s)',
    (direction, compact) => {
      const graph = fanGraph();
      const node = graph.nodes.find((n) => n.key === 'steps[1]')!;
      const plan = chatPlan(node, compact, direction)!;
      const anchors = chatAnchors(plan, direction, compact);
      const along = chatHandleAlong(plan, compact, direction);
      const lr = direction === 'LR';
      expect(lr ? anchors.target.left : anchors.target.top).toBe(along.enter);
      expect(lr ? anchors.source.left : anchors.source.top).toBe(along.exit);
    }
  );

  it.each(['TB', 'LR'] as const)(
    'enters and leaves a card by the middle of its box, outside the sub-nodes (%s)',
    (direction) => {
      const graph = fanGraph();
      const layout = layoutRoutes(graph, direction);
      const horizontal = direction === 'LR';
      for (const key of ['steps[1]', 'steps[2]']) {
        const node = graph.nodes.find((n) => n.key === key)!;
        const plan = chatPlan(node, false, direction)!;
        const at = layout.positions[key]!;
        const middle = horizontal
          ? at.y + plan.height / 2
          : at.x + plan.width / 2;
        const along = (p: { x: number; y: number }) => (horizontal ? p.x : p.y);
        const cross = (p: { x: number; y: number }) => (horizontal ? p.y : p.x);
        const into = graph.edges.find((e) => e.target === key)!;
        const out = graph.edges.find((e) => e.source === key)!;
        const entering = layout.waypoints[into.id] ?? [];
        const leaving = layout.waypoints[out.id] ?? [];
        const cardStart = horizontal ? at.x + plan.base.x : at.y + plan.card.y;
        const cardEnd = horizontal
          ? at.x + plan.base.x + plan.base.width
          : at.y + plan.base.y + plan.base.height;
        // The last point before the card is on the middle line, before the card.
        // A card that is flush with its box needs no point.
        const last = entering[entering.length - 1];
        if (last) {
          expect(cross(last)).toBeCloseTo(middle);
          expect(along(last)).toBeLessThan(cardStart);
        } else {
          expect(cardStart).toBe(horizontal ? at.x : at.y);
        }
        // The first point after the card is on the middle line, after the card.
        const first = leaving[0];
        if (first) {
          expect(cross(first)).toBeCloseTo(middle);
          expect(along(first)).toBeGreaterThan(cardEnd);
        } else {
          expect(cardEnd).toBe(
            horizontal ? at.x + plan.width : at.y + plan.height
          );
        }
      }
    }
  );
});

describe('buildCanvasGraph tools', () => {
  it('reads the tool entries of a chat step only', () => {
    const graph = chatGraph();
    const [fetch, triage] = graph.nodes as CanvasNodeWithRoutes[];
    expect(fetch?.tools).toBeUndefined();
    expect(fetch?.chat).toBeUndefined();
    expect(triage?.tools?.entries).toHaveLength(3);
  });

  // The list form `tools: [name]` is the released one; the canvas draws the same tools as for the key `dags`.
  it('reads the list form of tools as DAG entries', () => {
    const build = (extra: Record<string, unknown>) => {
      const model = makeModel([
        makeStep({
          key: 'steps[0]',
          kind: 'action',
          action: 'chat.completion',
          value: {
            action: 'chat.completion',
            with: { prompt: 'hi', ...extra },
          },
        }),
      ]);
      const graph = buildCanvasGraph({
        model,
        names: makeNames(model),
        issues: { byStep: {}, dag: [] },
      });
      return (graph.nodes[0] as CanvasNodeWithRoutes).tools;
    };
    const list = build({ tools: ['lookup', 'refund'] });
    const map = build({ dags: [{ name: 'lookup' }, { name: 'refund' }] });
    expect(list?.entries.map((entry) => [entry.kind, entry.label])).toEqual([
      ['dag', 'lookup'],
      ['dag', 'refund'],
    ]);
    expect(list?.entries.map((entry) => [entry.kind, entry.label])).toEqual(
      map?.entries.map((entry) => [entry.kind, entry.label])
    );
    expect(build({ tools: [] })).toBeUndefined();
  });

  it('gives a chat step without tools its model and no tool', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: {
          action: 'chat.completion',
          with: { prompt: 'hi', provider: 'openai', model: 'gpt-4o' },
        },
      }),
    ]);
    const graph = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
    const node = graph.nodes[0] as CanvasNodeWithRoutes;
    expect(node.tools).toBeUndefined();
    expect(node.chat?.models).toEqual([{ name: 'gpt-4o', provider: 'openai' }]);
  });
});

// The step `code` of examples/agent-worker-demo/dags/agent-dev-real.yaml.
const AGENT_CODE = {
  action: 'agent.run',
  with: {
    provider: 'claude',
    endpoint: { name: 'cle1', model: 'claude-haiku-4-5' },
    prompt: 'fix it',
    tools: {
      builtin: {
        read: true,
        edit: true,
        search: true,
        shell: {
          allow: ['./scripts/test.sh', 'git diff *'],
          deny: ['git push *'],
        },
      },
    },
    skills: ['review'],
  },
};

function agentGraph(
  value: Record<string, unknown> = AGENT_CODE,
  details?: CanvasGraphInput['details']
): CanvasGraph {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      explicitName: 'code',
      kind: 'action',
      action: 'agent.run',
      value,
    }),
  ]);
  return buildCanvasGraph({
    model,
    names: makeNames(model),
    issues: { byStep: {}, dag: [] },
    ...(details ? { details } : {}),
  });
}

describe('an agent.run step on the canvas', () => {
  const owner = 'steps[0]';

  it('reads its model, its tools with the built-in ones and its skills', () => {
    const node = agentGraph().nodes[0] as CanvasNodeWithRoutes;
    expect(node.chat?.models).toEqual([
      { name: 'claude-haiku-4-5', provider: 'claude', endpoint: 'cle1' },
    ]);
    expect(node.chat?.skills).toEqual([{ name: 'review', index: 0 }]);
    expect(node.tools?.entries).toEqual([]);
    expect(node.tools?.builtin?.map((entry) => entry.name)).toEqual([
      'read',
      'edit',
      'search',
      'shell',
    ]);
    expect(node.tools?.builtin?.[3]).toEqual({
      name: 'shell',
      allow: ['./scripts/test.sh', 'git diff *'],
      deny: ['git push *'],
    });
  });

  it('draws the model, the Prompt card, the Skills card, the Tools card with the built-in rows and the DAGs slot, in that order', () => {
    const drawn = withToolSatellites(agentGraph());
    expect(drawn.nodes.map((node) => node.key)).toEqual([
      owner,
      modelSatelliteKey(owner),
      promptCardKey(owner),
      slotCardKey(owner, 'skills'),
      slotCardKey(owner, 'tools'),
      // The step has no DAG tool: the DAGs slot is the small round "+".
      addSlotKey(owner, 'dags'),
    ]);
    const tools = drawn.nodes.find(
      (node) => node.key === slotCardKey(owner, 'tools')
    ) as CanvasNodeWithRoutes;
    expect(tools.slotCard?.entries).toHaveLength(0);
    expect(tools.slotCard?.builtin).toHaveLength(4);
    // Four rows, the add row, and the row of the limits of the step.
    expect(tools.slotCard?.editable).toBe(true);
    expect(tools.slotCard?.rows).toBe(6);
    expect(
      (
        withToolSatellites(agentGraph(), false).nodes.find(
          (node) => node.key === slotCardKey(owner, 'tools')
        ) as CanvasNodeWithRoutes
      ).slotCard?.editable
    ).toBe(false);
    const skills = drawn.nodes.find(
      (node) => node.key === slotCardKey(owner, 'skills')
    ) as CanvasNodeWithRoutes;
    expect(skills.slotCard?.skills).toEqual([{ name: 'review', index: 0 }]);
    expect(skills.slotCard?.editable).toBe(true);
  });

  // An agent step takes the tools of a chat step besides its built-in tools.
  it('reads the dag and MCP tools of an agent step, and draws the DAG tools on their own card', () => {
    const graph = agentGraph({
      action: 'agent.run',
      with: {
        provider: 'claude',
        dags: [{ name: 'lookup' }],
        tools: {
          mcp: [{ name: 'docs', url: 'https://docs.example.com/mcp' }],
          builtin: { read: true },
        },
      },
    });
    const node = graph.nodes[0] as CanvasNodeWithRoutes;
    expect(node.tools?.entries.map((entry) => entry.kind)).toEqual([
      'dag',
      'mcp',
    ]);
    expect(node.tools?.builtin).toEqual([{ name: 'read' }]);
    const drawn = withToolSatellites(graph);
    const card = drawn.nodes.find(
      (item) => item.key === slotCardKey(owner, 'tools')
    ) as CanvasNodeWithRoutes;
    // The Tools card holds the MCP server and the built-in tool, not the DAG.
    expect(card.slotCard?.entries.map((entry) => entry.kind)).toEqual(['mcp']);
    expect(card.slotCard?.builtin).toHaveLength(1);
    const dags = drawn.nodes.find(
      (item) => item.key === slotCardKey(owner, 'dags')
    ) as CanvasNodeWithRoutes;
    expect(dags.slotCard?.slot).toBe('dags');
    expect(dags.slotCard?.entries.map((entry) => entry.label)).toEqual([
      'lookup',
    ]);
    expect(dags.slotCard?.builtin).toBeUndefined();
  });

  it('gives the Tools card the limits and one more row for them', () => {
    const drawn = withToolSatellites(
      agentGraph({
        ...AGENT_CODE,
        with: { ...AGENT_CODE.with, max_tool_iterations: 20 },
      })
    );
    const card = drawn.nodes.find(
      (node) => node.key === slotCardKey(owner, 'tools')
    );
    const data = (card as CanvasNodeWithRoutes).slotCard;
    expect(data?.limits).toEqual({ turns: 20, calls: 100, callsDefault: true });
    // Four built-in tools, the add row, and the limits row.
    expect(data?.rows).toBe(4 + 1 + 1);
    const skills = drawn.nodes.find(
      (node) => node.key === slotCardKey(owner, 'skills')
    );
    expect((skills as CanvasNodeWithRoutes).slotCard?.limits).toBeUndefined();
  });

  it('draws the Prompt card with both texts, and a round "+" while both are empty', () => {
    const drawn = withToolSatellites(agentGraph());
    const card = drawn.nodes.find(
      (node) => node.key === promptCardKey(owner)
    ) as CanvasNodeWithRoutes;
    expect(card.promptCard).toEqual({
      owner,
      system: '',
      prompt: 'fix it',
      editable: true,
    });
    expect(
      withToolSatellites(agentGraph(), false).nodes.find(
        (node) => node.key === promptCardKey(owner)
      )
    ).toMatchObject({ promptCard: { editable: false } });
    const empty = withToolSatellites(
      agentGraph({ ...AGENT_CODE, with: { ...AGENT_CODE.with, prompt: ' ' } })
    );
    const plus = empty.nodes.find(
      (node) => node.key === addSlotKey(owner, 'prompt')
    ) as CanvasNodeWithRoutes;
    expect(plus.addSat).toEqual({ owner, slot: 'prompt' });
    expect(empty.nodes.some((node) => node.key === promptCardKey(owner))).toBe(
      false
    );
    // The Prompt is the second sub-node, after the Model.
    const second = (empty.nodes[0] as CanvasNodeWithRoutes).subRow?.items[1];
    expect(second).toMatchObject({ slot: 'prompt', empty: true });
    expect(satelliteOwner(promptCardKey(owner))).toBe(owner);
  });

  it('draws no Prompt card for a chat step', () => {
    const drawn = withToolSatellites(chatGraph());
    expect(
      drawn.nodes.some((node) => (node as CanvasNodeWithRoutes).promptCard)
    ).toBe(false);
    expect(drawn.nodes.some((node) => node.key.endsWith(':add-prompt'))).toBe(
      false
    );
  });

  // Top to bottom, the Model and the Prompt are the left column, and the Skills
  // and the Tools the right column, beside the step. Left to right, the Model
  // and the Prompt are a row above the step, and the Skills and the Tools a
  // row under it. Nothing overlaps.
  it('places the five sub-nodes beside the step, or above and under it, in both sizes', () => {
    const longName = 'a-very-long-name-that-never-ends-'.repeat(4);
    const values: Record<string, unknown>[] = [
      // Four full sub-nodes.
      AGENT_CODE,
      // Empty Prompt, Skills and Tools: three small round "+".
      { action: 'agent.run', with: { provider: 'claude', prompt: ' ' } },
      // Long names in every sub-node.
      {
        action: 'agent.run',
        with: {
          provider: 'claude',
          model: longName,
          prompt: longName,
          system: longName,
          skills: [longName],
          dags: [{ name: longName }],
          tools: {
            builtin: { read: true, shell: { allow: [longName] } },
          },
        },
      },
    ];
    for (const value of values) {
      for (const direction of ['TB', 'LR'] as const) {
        for (const compact of [false, true]) {
          const drawn = withToolSatellites(agentGraph(value));
          const positions = layoutGraph(drawn, direction, undefined, compact);
          const box = (key: string) => {
            const node = drawn.nodes.find((n) => n.key === key)!;
            const at = positions[key]!;
            return { x: at.x, y: at.y, ...nodeSize(node, compact, direction) };
          };
          const plan = chatPlan(drawn.nodes[0]!, compact, direction)!;
          const stepAt = positions[owner]!;
          const card = {
            x: stepAt.x + plan.base.x,
            y: stepAt.y + plan.base.y,
            width: plan.base.width,
            height: plan.base.height,
          };
          const shape = drawn.nodes[0] as CanvasNodeWithRoutes;
          const bySlot = Object.fromEntries(
            shape.subRow!.items.map((item) => [item.slot, box(item.key)])
          );
          const { model, prompt, skills, tools, dags } = bySlot as Record<
            'model' | 'prompt' | 'skills' | 'tools' | 'dags',
            ReturnType<typeof box>
          >;
          const label = `${direction} ${compact ? 'compact' : 'normal'}`;
          expect(
            shape.subRow!.items.map((item) => item.slot),
            label
          ).toEqual(['model', 'prompt', 'skills', 'tools', 'dags']);
          if (direction === 'TB') {
            // Top to bottom in the left column, and in the right one.
            expect(model.y + model.height, label).toBeLessThanOrEqual(prompt.y);
            expect(skills.y + skills.height, label).toBeLessThanOrEqual(
              tools.y
            );
            expect(tools.y + tools.height, label).toBeLessThanOrEqual(dags.y);
            for (const left of [model, prompt]) {
              expect(left.x + left.width, label).toBeLessThanOrEqual(card.x);
            }
            for (const right of [skills, tools, dags]) {
              expect(right.x, label).toBeGreaterThanOrEqual(
                card.x + card.width
              );
            }
          } else {
            // Left to right in each row.
            expect(model.x + model.width, label).toBeLessThanOrEqual(prompt.x);
            expect(skills.x + skills.width, label).toBeLessThanOrEqual(tools.x);
            expect(tools.x + tools.width, label).toBeLessThanOrEqual(dags.x);
            // The Model and the Prompt above the step, the Skills, the Tools and the DAGs under it.
            for (const above of [model, prompt]) {
              expect(above.y + above.height, label).toBeLessThanOrEqual(card.y);
            }
            for (const under of [skills, tools, dags]) {
              expect(under.y, label).toBeGreaterThanOrEqual(
                card.y + card.height
              );
            }
          }
          const all = [model, prompt, skills, tools, dags];
          all.forEach((a, i) => {
            const inside =
              a.x >= stepAt.x &&
              a.y >= stepAt.y &&
              a.x + a.width <=
                stepAt.x +
                  nodeSize(drawn.nodes[0]!, compact, direction).width &&
              a.y + a.height <=
                stepAt.y + nodeSize(drawn.nodes[0]!, compact, direction).height;
            expect(inside, `${label} inside`).toBe(true);
            all.slice(i + 1).forEach((b) => {
              const overlaps =
                a.x < b.x + b.width &&
                b.x < a.x + a.width &&
                a.y < b.y + b.height &&
                b.y < a.y + a.height;
              expect(overlaps, label).toBe(false);
            });
          });
        }
      }
    }
  });

  it('draws the DAGs card, and no Tools card, for an agent step that has only a DAG tool', () => {
    const graph = agentGraph({
      action: 'agent.run',
      with: { provider: 'claude', dags: [{ name: 'lookup' }] },
    });
    const node = graph.nodes[0] as CanvasNodeWithRoutes;
    expect(node.tools?.entries).toHaveLength(1);
    expect(node.tools?.builtin).toEqual([]);
    const keys = withToolSatellites(graph).nodes.map((item) => item.key);
    expect(keys).toContain(slotCardKey(owner, 'dags'));
    expect(keys).not.toContain(slotCardKey(owner, 'tools'));
    expect(keys).toContain(addSlotKey(owner, 'tools'));
  });

  // A step that names no model has a Model node with the pickers, and no
  // model of its own to name.
  it('gives a step that names no model a Model node with no model', () => {
    const drawn = withToolSatellites(
      agentGraph({ action: 'agent.run', with: { prompt: 'x' } })
    );
    const model = drawn.nodes.find(
      (node) => node.key === modelSatelliteKey(owner)
    ) as CanvasNodeWithRoutes;
    expect(model.modelSat?.models).toEqual([]);
    expect(model.modelSat?.card).toBeDefined();
    expect(drawn.nodes.map((node) => node.key)).not.toContain(
      addSatelliteKey(owner)
    );
  });

  it('draws the pickers while the step names no provider and no model, and the dashed "+" with no controls', () => {
    const graph = agentGraph({ action: 'agent.run', with: { prompt: 'x' } });
    const keys = (controls: boolean) =>
      withToolSatellites(graph, true, controls).nodes.map((node) => node.key);
    expect(keys(true)).toContain(modelSatelliteKey(owner));
    expect(keys(true)).not.toContain(addSatelliteKey(owner));
    expect(keys(false)).toContain(addSatelliteKey(owner));
    expect(keys(false)).not.toContain(modelSatelliteKey(owner));
  });

  it('keeps the built-in tools off the card of a chat step', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: {
          action: 'chat.completion',
          with: { tools: { builtin: { read: true } } },
        },
      }),
    ]);
    const graph = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
    const node = graph.nodes[0] as CanvasNodeWithRoutes;
    expect(node.tools).toBeUndefined();
    // The step has no tool to list: its Tools slot is the small round "+".
    const keys = withToolSatellites(graph).nodes.map((item) => item.key);
    expect(keys).not.toContain(slotCardKey('steps[0]', 'tools'));
    expect(keys).toContain(addSlotKey('steps[0]', 'tools'));
  });
});

describe('withToolSatellites', () => {
  const owner = 'steps[1]';

  it('returns the graph when it has no chat step', () => {
    const model = makeModel([
      makeStep({ key: 'steps[0]', explicitName: 'a', value: { run: 'x' } }),
    ]);
    const graph = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
    expect(withToolSatellites(graph)).toBe(graph);
  });

  it('adds the model and a node for each of the Skills, Tools and DAGs slots, always, after the step', () => {
    const graph = chatGraph();
    const drawn = withToolSatellites(graph);
    expect(drawn.nodes.map((node) => node.key)).toEqual([
      'steps[0]',
      owner,
      modelSatelliteKey(owner),
      addSlotKey(owner, 'skills'),
      slotCardKey(owner, 'tools'),
      slotCardKey(owner, 'dags'),
      'steps[2]',
    ]);
    const added = drawn.edges.filter(isToolSatelliteEdge);
    expect(added.map((edge) => [edge.source, edge.target, edge.label])).toEqual(
      [
        [owner, modelSatelliteKey(owner), 'model'],
        [owner, addSlotKey(owner, 'skills'), 'skills'],
        [owner, slotCardKey(owner, 'tools'), 'tools'],
        [owner, slotCardKey(owner, 'dags'), 'dags'],
      ]
    );
    // The graph of the edits keeps its nodes and edges.
    expect(graph.nodes).toHaveLength(3);
    expect(graph.edges.some(isToolSatelliteEdge)).toBe(false);
    expect((graph.nodes[1] as CanvasNodeWithRoutes).subRow).toBeUndefined();
  });

  it('draws the model of the step, and a dashed "+" while there is none in a canvas with no controls', () => {
    const named = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: {
          action: 'chat.completion',
          with: { model: [{ provider: 'openai', name: 'a' }, 'b'] },
        },
      }),
    ]);
    const base = buildCanvasGraph({
      model: named,
      names: makeNames(named),
      issues: { byStep: {}, dag: [] },
    });
    const drawn = withToolSatellites(base, false);
    const model = drawn.nodes.find(
      (node) => node.key === modelSatelliteKey('steps[0]')
    ) as CanvasNodeWithRoutes;
    expect(model.modelSat?.models.map((item) => item.name)).toEqual(['a', 'b']);
    expect(isModelSubNode(model)).toBe(true);
    const empty = withToolSatellites(chatGraph(), false);
    const add = empty.nodes.find(
      (node) => node.key === addSatelliteKey(owner)
    ) as CanvasNodeWithRoutes;
    expect(add.addSat).toEqual({ owner, slot: 'model' });
    expect(isModelSubNode(add)).toBe(true);
    // The edit view has the pickers of the Model node in its place.
    const picked = withToolSatellites(chatGraph(), true, true);
    expect(
      picked.nodes.some((node) => node.key === addSatelliteKey(owner))
    ).toBe(false);
    const card = picked.nodes.find(
      (node) => node.key === modelSatelliteKey(owner)
    ) as CanvasNodeWithRoutes;
    expect(card.modelSat?.models).toEqual([]);
    expect(card.modelSat?.card).toEqual({
      rows: 1,
      cli: false,
      effort: true,
    });
    expect(isModelSubNode(card)).toBe(true);
  });

  it('gives each slot card its entries, its rows and an add row only when editable', () => {
    const editable = withToolSatellites(chatGraph());
    const tools = editable.nodes.find(
      (node) => node.key === slotCardKey(owner, 'tools')
    ) as CanvasNodeWithRoutes;
    expect(tools.slotCard?.entries.map((entry) => entry.kind)).toEqual(['mcp']);
    // One entry of one kind (no heading), and the add row.
    expect(tools.slotCard?.rows).toBe(2);
    expect(isModelSubNode(tools)).toBe(false);
    const dags = editable.nodes.find(
      (node) => node.key === slotCardKey(owner, 'dags')
    ) as CanvasNodeWithRoutes;
    expect(dags.slotCard?.entries.map((entry) => entry.label)).toEqual([
      'lookup',
      'refund',
    ]);
    // The rows under the entries: the add row only.
    expect(dags.slotCard?.rows).toBe(1);
    // The step has no skill: the Skills slot is the small round "+", no card.
    expect(
      editable.nodes.some((node) => node.key === slotCardKey(owner, 'skills'))
    ).toBe(false);

    const readOnly = withToolSatellites(chatGraph(), false);
    const fixed = readOnly.nodes.find(
      (node) => node.key === slotCardKey(owner, 'tools')
    ) as CanvasNodeWithRoutes;
    expect(fixed.slotCard?.rows).toBe(1);
    expect(fixed.slotCard?.editable).toBe(false);
    const fixedDags = readOnly.nodes.find(
      (node) => node.key === slotCardKey(owner, 'dags')
    ) as CanvasNodeWithRoutes;
    expect(fixedDags.slotCard?.rows).toBe(0);
  });

  it('reads the names of with.skills, with their place in the list', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: {
          action: 'chat.completion',
          with: { skills: ['refund-policy', 3, 'tone-of-voice'] },
        },
      }),
    ]);
    const base = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
    const drawn = withToolSatellites(base);
    const card = drawn.nodes.find(
      (node) => node.key === slotCardKey('steps[0]', 'skills')
    ) as CanvasNodeWithRoutes;
    expect(card.slotCard?.skills).toEqual([
      { name: 'refund-policy', index: 0 },
      { name: 'tone-of-voice', index: 2 },
    ]);
    expect(card.slotCard?.rows).toBe(3);
  });

  it('gives the chat step the shape of its row', () => {
    const drawn = withToolSatellites(chatGraph());
    const step = drawn.nodes.find(
      (node) => node.key === owner
    ) as CanvasNodeWithRoutes;
    const rest = [
      { key: addSlotKey(owner, 'skills'), slot: 'skills', empty: true },
      { key: slotCardKey(owner, 'tools'), slot: 'tools', rows: 2 },
      {
        key: slotCardKey(owner, 'dags'),
        slot: 'dags',
        rows: 1,
        dagCard: { entries: 2, rows: 1 },
      },
    ];
    expect(step.subRow?.items).toEqual([
      {
        key: modelSatelliteKey(owner),
        slot: 'model',
        modelCard: { rows: 1, cli: false, effort: true },
      },
      ...rest,
    ]);
    // A canvas with no controls has the dashed "+", which has no card.
    const plain = withToolSatellites(chatGraph(), true, false).nodes.find(
      (node) => node.key === owner
    ) as CanvasNodeWithRoutes;
    expect(plain.subRow?.items).toEqual([
      { key: addSatelliteKey(owner), slot: 'model' },
      ...rest,
    ]);
  });

  it('draws a small round "+" for a slot with no entry, and the card again with the first entry', () => {
    const build = (withBlock: Record<string, unknown>) => {
      const model = makeModel([
        makeStep({
          key: 'steps[0]',
          kind: 'action',
          action: 'chat.completion',
          value: { action: 'chat.completion', with: withBlock },
        }),
      ]);
      return withToolSatellites(
        buildCanvasGraph({
          model,
          names: makeNames(model),
          issues: { byStep: {}, dag: [] },
        })
      );
    };
    const keys = (graph: CanvasGraph) => graph.nodes.map((node) => node.key);
    const empty = build({ prompt: 'hi' });
    expect(keys(empty)).toEqual([
      'steps[0]',
      modelSatelliteKey('steps[0]'),
      addSlotKey('steps[0]', 'skills'),
      addSlotKey('steps[0]', 'tools'),
      addSlotKey('steps[0]', 'dags'),
    ]);
    const tools = empty.nodes.find(
      (node) => node.key === addSlotKey('steps[0]', 'tools')
    ) as CanvasNodeWithRoutes;
    expect(tools.addSat).toEqual({ owner: 'steps[0]', slot: 'tools' });
    expect(tools.slotCard).toBeUndefined();
    expect(isModelSubNode(tools)).toBe(false);
    // The "+" is a sub-node that the step links to, like the card.
    expect(isToolSatelliteKey(tools.key)).toBe(true);
    expect(satelliteOwner(tools.key)).toBe('steps[0]');
    expect(toolSatelliteFlowEdges(empty).map((edge) => edge.target)).toContain(
      tools.key
    );

    const withTool = build({
      prompt: 'hi',
      tools: { mcp: [{ name: 'a', url: 'https://mcp.example.com' }] },
    });
    expect(keys(withTool)).toContain(slotCardKey('steps[0]', 'tools'));
    expect(keys(withTool)).toContain(addSlotKey('steps[0]', 'skills'));
    expect(keys(withTool)).toContain(addSlotKey('steps[0]', 'dags'));
    expect(keys(withTool)).not.toContain(addSlotKey('steps[0]', 'tools'));

    // A DAG tool has its own slot: the Tools slot stays a "+".
    const withDag = build({ prompt: 'hi', dags: [{ name: 'a' }] });
    expect(keys(withDag)).toContain(slotCardKey('steps[0]', 'dags'));
    expect(keys(withDag)).not.toContain(addSlotKey('steps[0]', 'dags'));
    expect(keys(withDag)).toContain(addSlotKey('steps[0]', 'tools'));

    const withSkill = build({ prompt: 'hi', skills: ['a'] });
    expect(keys(withSkill)).toContain(slotCardKey('steps[0]', 'skills'));
    expect(keys(withSkill)).toContain(addSlotKey('steps[0]', 'tools'));
    expect(keys(withSkill)).not.toContain(addSlotKey('steps[0]', 'skills'));
  });

  it('tells the owner from a sub-node key', () => {
    for (const key of [
      slotCardKey(owner, 'tools'),
      slotCardKey(owner, 'skills'),
      slotCardKey(owner, 'dags'),
      addSlotKey(owner, 'tools'),
      addSlotKey(owner, 'skills'),
      addSlotKey(owner, 'dags'),
      modelSatelliteKey(owner),
      addSatelliteKey(owner),
    ]) {
      expect(isToolSatelliteKey(key)).toBe(true);
      expect(satelliteOwner(key)).toBe(owner);
    }
    expect(isToolSatelliteKey(owner)).toBe(false);
    expect(satelliteOwner(owner)).toBeNull();
  });

  it('moves the sub-nodes with their step', () => {
    const drawn = withToolSatellites(chatGraph());
    const positions = layoutGraph(drawn, 'TB');
    const first = slotCardKey(owner, 'tools');
    const moved = withFollowers(drawn, positions, {
      [owner]: { x: positions[owner]!.x + 30, y: positions[owner]!.y - 10 },
    });
    expect(moved[owner]).toEqual({
      x: positions[owner]!.x + 30,
      y: positions[owner]!.y - 10,
    });
    expect(moved[first]).toEqual({
      x: positions[first]!.x + 30,
      y: positions[first]!.y - 10,
    });
    expect(Object.keys(moved)).toHaveLength(1 + 4);
    expect(moved[addSlotKey(owner, 'skills')]).toEqual({
      x: positions[addSlotKey(owner, 'skills')]!.x + 30,
      y: positions[addSlotKey(owner, 'skills')]!.y - 10,
    });
  });
});

describe('layout of the sub-nodes', () => {
  const owner = 'steps[1]';
  const graph = withToolSatellites(chatGraph());

  function boxes(
    direction: 'TB' | 'LR',
    compact: boolean,
    drawn: CanvasGraph = graph
  ) {
    const positions = layoutGraph(drawn, direction, undefined, compact);
    return drawn.nodes.map((node) => ({
      key: node.key,
      ...positions[node.key]!,
      ...nodeSize(node, compact, direction),
    }));
  }

  const apart = (
    a: { x: number; y: number; width: number; height: number },
    b: { x: number; y: number; width: number; height: number }
  ) =>
    a.x + a.width <= b.x ||
    b.x + b.width <= a.x ||
    a.y + a.height <= b.y ||
    b.y + b.height <= a.y;

  // The box of a step hangs out of its rank, along the flow, over the steps
  // next to it: the steps and the sub-nodes of two steps must not touch, but
  // their boxes can.
  function drawn(
    direction: 'TB' | 'LR',
    compact: boolean,
    on: CanvasGraph = graph
  ) {
    return boxes(direction, compact, on).map((box) => {
      const node = on.nodes.find((n) => n.key === box.key)!;
      const plan = chatPlan(node, compact, direction);
      return plan
        ? {
            ...box,
            x: box.x + plan.base.x,
            y: box.y + plan.base.y,
            width: plan.base.width,
            height: plan.base.height,
          }
        : box;
    });
  }

  it('puts the sub-nodes beside their step, inside its box, without overlap, top to bottom', () => {
    for (const compact of [false, true]) {
      const all = boxes('TB', compact);
      const step = all.find((box) => box.key === owner)!;
      const plan = chatPlan(
        graph.nodes.find((node) => node.key === owner)!,
        compact,
        'TB'
      )!;
      const card = {
        x: step.x + plan.base.x,
        y: step.y + plan.base.y,
        width: plan.base.width,
        height: plan.base.height,
      };
      const subs = all.filter((b) => isToolSatelliteKey(b.key));
      expect(subs.length).toBeGreaterThan(0);
      for (const box of subs) {
        // Inside the box of the step, and off the step itself.
        expect(box.y).toBeGreaterThanOrEqual(step.y);
        expect(box.y + box.height).toBeLessThanOrEqual(step.y + step.height);
        expect(box.x).toBeGreaterThanOrEqual(step.x);
        expect(box.x + box.width).toBeLessThanOrEqual(step.x + step.width);
        expect(apart(box, card), box.key).toBe(true);
        // Beside the step: left of it, or right of it.
        expect(
          box.x + box.width <= card.x || box.x >= card.x + card.width,
          box.key
        ).toBe(true);
      }
      const shown = drawn('TB', compact);
      for (const a of shown) {
        for (const b of shown) {
          if (a.key >= b.key) continue;
          // The box of the step holds its sub-nodes.
          if (
            satelliteOwner(a.key) === b.key ||
            satelliteOwner(b.key) === a.key
          ) {
            continue;
          }
          expect(apart(a, b), `${a.key} and ${b.key}`).toBe(true);
        }
      }
    }
  });

  it('puts the sub-nodes above and under their step, inside its box, without overlap, left to right', () => {
    for (const compact of [false, true]) {
      const all = boxes('LR', compact);
      const step = all.find((box) => box.key === owner)!;
      const plan = chatPlan(
        graph.nodes.find((node) => node.key === owner)!,
        compact,
        'LR'
      )!;
      const upper = step.y + plan.base.y;
      const lower = step.y + plan.base.y + plan.base.height;
      for (const box of all.filter((b) => isToolSatelliteKey(b.key))) {
        // Above the step, or under it.
        expect(box.y + box.height <= upper || box.y >= lower, box.key).toBe(
          true
        );
        expect(box.y + box.height).toBeLessThanOrEqual(step.y + step.height);
        expect(box.x).toBeGreaterThanOrEqual(step.x);
        expect(box.x + box.width).toBeLessThanOrEqual(step.x + step.width);
      }
      const shown = drawn('LR', compact);
      for (const a of shown) {
        for (const b of shown) {
          if (a.key >= b.key) continue;
          if (
            satelliteOwner(a.key) === b.key ||
            satelliteOwner(b.key) === a.key
          ) {
            continue;
          }
          expect(apart(a, b), `${a.key} and ${b.key}`).toBe(true);
        }
      }
    }
  });

  // A chat.completion step has no Prompt card. Top to bottom, the Model is
  // alone in the left column, and the Skills are above the Tools in the right
  // one. Left to right, the Model is above the step, and the Skills are left
  // of the Tools under it. The Model is the card of pickers, or the dashed "+"
  // in a canvas with no controls.
  it.each([
    ['the card of pickers', true, modelSatelliteKey(owner)],
    ['the dashed "+"', false, addSatelliteKey(owner)],
  ])(
    'puts the Model first, then the Skills and the Tools, with %s, in both directions',
    (_name, controls, modelKey) => {
      const drawn = withToolSatellites(chatGraph(), true, controls);
      for (const direction of ['TB', 'LR'] as const) {
        for (const compact of [false, true]) {
          const all = boxes(direction, compact, drawn);
          const at = (key: string) => all.find((box) => box.key === key)!;
          const model = at(modelKey);
          const skills = at(addSlotKey(owner, 'skills'));
          const tools = at(slotCardKey(owner, 'tools'));
          const step = at(owner);
          const label = `${direction} ${compact}`;
          if (direction === 'TB') {
            expect(model.x + model.width, label).toBeLessThanOrEqual(skills.x);
            expect(skills.x, label).toBe(tools.x);
            expect(skills.y + skills.height, label).toBeLessThanOrEqual(
              tools.y
            );
          } else {
            expect(model.y + model.height, label).toBeLessThan(
              step.y + step.height / 2
            );
            expect(skills.y, label).toBeGreaterThan(step.y + step.height / 2);
            expect(skills.y, label).toBe(tools.y);
            expect(skills.x + skills.width, label).toBeLessThanOrEqual(tools.x);
          }
        }
      }
    }
  );

  it('gives the Model card of a step with no model its width and its height, inside the box of the step', () => {
    for (const direction of ['TB', 'LR'] as const) {
      const all = boxes(direction, false);
      const model = all.find((box) => box.key === modelSatelliteKey(owner))!;
      expect(model.width).toBe(MODEL_CARD_WIDTH);
      expect(model.height).toBe(modelCardHeight(1, false, true));
      const step = all.find((box) => box.key === owner)!;
      const plan = chatPlan(
        graph.nodes.find((node) => node.key === owner) as CanvasNodeWithRoutes,
        false,
        direction
      )!;
      const inside = plan.items[modelSatelliteKey(owner)]!;
      expect(inside.width).toBe(MODEL_CARD_WIDTH);
      expect(inside.height).toBe(modelCardHeight(1, false, true));
      expect(inside.x).toBeGreaterThanOrEqual(0);
      expect(inside.y).toBeGreaterThanOrEqual(0);
      expect(inside.x + inside.width).toBeLessThanOrEqual(plan.width);
      expect(inside.y + inside.height).toBeLessThanOrEqual(plan.height);
      expect(step.width).toBeGreaterThan(0);
    }
  });

  it('keeps the steps around a chat step off its sub-nodes, in both directions', () => {
    for (const direction of ['TB', 'LR'] as const) {
      for (const compact of [false, true]) {
        const all = boxes(direction, compact);
        const subs = all.filter((b) => isToolSatelliteKey(b.key));
        for (const other of ['steps[0]', 'steps[2]']) {
          const box = all.find((b) => b.key === other)!;
          for (const sub of subs) {
            expect(apart(box, sub), `${other} and ${sub.key}`).toBe(true);
          }
        }
      }
    }
  });

  it('makes the box wider top to bottom, and taller left to right, than the card', () => {
    const node = graph.nodes.find((n) => n.key === owner)!;
    const tb = nodeSize(node, false, 'TB');
    const lr = nodeSize(node, false, 'LR');
    const card = nodeSize(chatGraph().nodes[1]!, false, 'TB');
    expect(tb.width).toBeGreaterThan(card.width);
    expect(lr.height).toBeGreaterThan(card.height);
    expect(chatPlan(node, false, 'TB')!.slots.tools.side).toBe('right');
    expect(chatPlan(node, false, 'LR')!.slots.tools.side).toBe('bottom');
    expect(chatPlan(node, false, 'LR')!.slots.model.side).toBe('top');
  });

  // The ranks count the card, not the sub-nodes: the step after it is as near
  // as after a chat step that has none.
  it('puts the next step under the card of its step, as near as under a plain one', () => {
    const shown = drawn('TB', false);
    const step = shown.find((box) => box.key === owner)!;
    const after = shown.find((box) => box.key === 'steps[2]')!;
    const plain = layoutGraph(chatGraph(), 'TB');
    const plainSize = nodeSize(chatGraph().nodes[1]!, false, 'TB');
    expect(after.y - (step.y + step.height)).toBe(
      plain['steps[2]']!.y - (plain[owner]!.y + plainSize.height)
    );
  });

  it('is the same for the next step in every direction and mode', () => {
    for (const direction of ['TB', 'LR'] as const) {
      for (const compact of [false, true]) {
        const positions = layoutGraph(graph, direction, undefined, compact);
        expect(Object.keys(positions)).toHaveLength(graph.nodes.length);
        const all = boxes(direction, compact);
        const step = all.find((box) => box.key === owner)!;
        for (const box of all.filter((b) => isToolSatelliteKey(b.key))) {
          expect(box.y).toBeGreaterThanOrEqual(step.y);
          expect(box.y + box.height).toBeLessThanOrEqual(step.y + step.height);
        }
      }
    }
  });

  it('gives the card place back for the export', () => {
    const positions = layoutGraph(graph, 'TB');
    const cards = cardPositions(graph, positions, false, 'TB');
    const plan = chatPlan(
      graph.nodes.find((node) => node.key === owner)!,
      false,
      'TB'
    )!;
    expect(cards[owner]).toEqual({
      x: positions[owner]!.x + plan.base.x,
      y: positions[owner]!.y + plan.base.y,
    });
    expect(cards['steps[0]']).toEqual(positions['steps[0]']);
  });

  it('makes flow nodes of the right types and edges that are not editable', () => {
    const positions = layoutGraph(graph, 'TB');
    const nodes = toFlowNodes(graph, positions, null);
    const types = Object.fromEntries(nodes.map((n) => [n.id, n.type]));
    expect(types[slotCardKey(owner, 'tools')]).toBe('slotcard');
    // The DAGs card is its own node type, with a miniature in each entry.
    expect(types[slotCardKey(owner, 'dags')]).toBe('dagcard');
    // The step has no skill: its Skills slot is the small round "+".
    expect(types[addSlotKey(owner, 'skills')]).toBe('addsat');
    expect(types[modelSatelliteKey(owner)]).toBe('modelsat');
    expect(types[owner]).toBe('step');
    // A canvas with no controls draws the dashed "+" of the Model slot.
    const plain = withToolSatellites(chatGraph(), true, false);
    const plainTypes = Object.fromEntries(
      toFlowNodes(plain, layoutGraph(plain, 'TB'), null).map((n) => [
        n.id,
        n.type,
      ])
    );
    expect(plainTypes[addSatelliteKey(owner)]).toBe('addsat');
    // A chat step with a step after it is not a sink.
    expect(nodes.find((n) => n.id === owner)?.data.isSink).toBe(false);
    const flowEdges = toolSatelliteFlowEdges(graph);
    expect(flowEdges).toHaveLength(4);
    expect(flowEdges.every((edge) => edge.type === 'toolsat')).toBe(true);
    expect(flowEdges.map((edge) => edge.sourceHandle)).toEqual([
      'slot:model',
      'slot:skills',
      'slot:tools',
      'slot:dags',
    ]);
    expect(
      toFlowEdges(chatGraph(), { editable: true, selectedEdgeId: null }).some(
        (edge) => isToolSatelliteEdge(edge)
      )
    ).toBe(false);
  });

  it('makes a model node when the step names a model', () => {
    const named = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: { action: 'chat.completion', with: { model: 'gpt-4o' } },
      }),
    ]);
    const base = buildCanvasGraph({
      model: named,
      names: makeNames(named),
      issues: { byStep: {}, dag: [] },
    });
    const drawn = withToolSatellites(base);
    const nodes = toFlowNodes(drawn, layoutGraph(drawn, 'TB'), null);
    expect(
      nodes.find((n) => n.id === modelSatelliteKey('steps[0]'))?.type
    ).toBe('modelsat');
  });

  it('keeps the sink stalk of a chat step that has only sub-nodes', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: CHAT,
      }),
    ]);
    const base = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
    const drawn = withToolSatellites(base);
    const nodes = toFlowNodes(drawn, layoutGraph(drawn, 'TB'), null);
    expect(nodes.find((n) => n.id === 'steps[0]')?.data.isSink).toBe(true);
  });

  it('makes the DAGs card as tall as its entries and keeps it inside the box of the step', () => {
    const dags = Array.from({ length: 12 }, (_, i) => ({ name: `t${i}` }));
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: { action: 'chat.completion', with: { dags } },
      }),
    ]);
    const base = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
    const drawn = withToolSatellites(base);
    const plan = chatPlan(drawn.nodes[0]!, false, 'TB')!;
    const card = plan.items[slotCardKey('steps[0]', 'dags')]!;
    const skills = plan.items[addSlotKey('steps[0]', 'skills')]!;
    // Three entries with a miniature each, "+9 more" and the add row.
    expect(card.height).toBe(dagCardHeight(3, 2));
    expect(card.width).toBe(DAG_CARD_WIDTH);
    expect(card.height).toBeGreaterThan(skills.height);
    expect(plan.height).toBeGreaterThanOrEqual(card.y + card.height);
    // A compact canvas draws a tile for the card.
    const compact = chatPlan(drawn.nodes[0]!, true, 'TB')!;
    expect(compact.items[slotCardKey('steps[0]', 'dags')]).toMatchObject({
      width: SUB_TILE_SIZE,
      height: SUB_TILE_SIZE,
    });
  });
});

// A step with sub-nodes takes the room of its card in a rank, and its
// sub-nodes hang out into the gaps and the ranks next to it. The gaps grow only
// where a sub-node would touch another card, a sub-node or an edge.
describe('the rank spacing of a step with sub-nodes', () => {
  // The Model, Prompt, Skills, Tools and DAGs cards: the tallest column.
  const FULL = {
    action: 'agent.run',
    with: {
      provider: 'claude',
      endpoint: { name: 'cle1', model: 'claude-haiku-4-5' },
      prompt: 'fix it',
      skills: ['a', 'b'],
      dags: [{ name: 'lookup' }, { name: 'refund' }],
      tools: {
        mcp: [{ name: 'docs', url: 'https://x', allow: ['s'] }],
        builtin: { read: true, edit: true, search: true },
      },
    },
  };
  const MODEL_ONLY = {
    action: 'chat.completion',
    with: { prompt: 'hi', endpoint: { name: 'e', model: 'm' } },
  };
  const BARE = { action: 'chat.completion', with: { prompt: 'hi' } };

  type Spec = { name: string; depends: string[]; value?: object };
  const plain = (name: string, ...depends: string[]): Spec => ({
    name,
    depends,
  });
  const agent = (name: string, depends: string[], value = FULL): Spec => ({
    name,
    depends,
    value,
  });

  function build(spec: Spec[]): CanvasGraph {
    const model = makeModel(
      spec.map((step, i) =>
        makeStep({
          key: `steps[${i}]`,
          explicitName: step.name,
          depends: step.depends,
          ...(step.value
            ? {
                kind: 'action',
                action: (step.value as { action: string }).action,
                value: step.value as Record<string, unknown>,
              }
            : { value: { run: 'x' } }),
        })
      )
    );
    return withToolSatellites(
      buildCanvasGraph({
        model,
        names: makeNames(model),
        issues: { byStep: {}, dag: [] },
      })
    );
  }

  const MODES = [
    ['TB', false],
    ['TB', true],
    ['LR', false],
    ['LR', true],
  ] as const;

  // The cards of the steps and the sub-nodes, by their drawn places.
  function rects(graph: CanvasGraph, direction: 'TB' | 'LR', compact: boolean) {
    const positions = layoutGraph(graph, direction, undefined, compact);
    return graph.nodes.map((node) => {
      const at = positions[node.key]!;
      const plan = chatPlan(node, compact, direction);
      const box = plan
        ? plan.base
        : { x: 0, y: 0, ...nodeSize(node, compact, direction) };
      return {
        key: node.key,
        owner: satelliteOwner(node.key) ?? node.key,
        x: at.x + box.x,
        y: at.y + box.y,
        width: box.width,
        height: box.height,
      };
    });
  }

  const CASES: [string, Spec[]][] = [
    ['a chain', [plain('a'), agent('b', ['a']), plain('c', 'b')]],
    ['a chain of two agent steps', [agent('a', []), agent('b', ['a'])]],
    [
      'a chain of a model-only step and a bare step',
      [
        agent('a', [], MODEL_ONLY as typeof FULL),
        agent('b', ['a'], BARE as typeof FULL),
        plain('c', 'b'),
      ],
    ],
    [
      'two agent steps in parallel',
      [plain('s'), agent('x', ['s']), agent('y', ['s']), plain('j', 'x', 'y')],
    ],
    [
      'a fan of three agent steps',
      [
        plain('s'),
        agent('x', ['s']),
        agent('y', ['s']),
        agent('z', ['s']),
        plain('j', 'x', 'y', 'z'),
      ],
    ],
    [
      'an agent step beside a plain branch',
      [
        plain('s'),
        agent('x', ['s']),
        plain('p', 's'),
        plain('q', 'p'),
        plain('j', 'x', 'q'),
      ],
    ],
    [
      'a plain step in the rank of the neighbour of an agent step',
      [plain('a'), agent('b', ['a']), plain('q'), plain('c', 'b', 'q')],
    ],
    [
      'an edge that skips an agent step',
      [plain('a'), agent('b', ['a']), plain('c', 'b', 'a')],
    ],
    [
      'two chains side by side',
      [
        plain('a'),
        agent('b', ['a']),
        plain('c', 'b'),
        plain('d'),
        agent('e', ['d']),
        plain('f', 'e'),
      ],
    ],
  ];

  it.each(CASES)('touches no card or sub-node: %s', (_name, spec) => {
    const graph = build(spec);
    for (const [direction, compact] of MODES) {
      const all = rects(graph, direction, compact);
      for (const a of all) {
        for (const b of all) {
          if (a.key >= b.key || a.owner === b.owner) continue;
          const apart =
            a.x + a.width <= b.x ||
            b.x + b.width <= a.x ||
            a.y + a.height <= b.y ||
            b.y + b.height <= a.y;
          expect(apart, `${direction} ${compact}: ${a.key} and ${b.key}`).toBe(
            true
          );
        }
      }
    }
  });

  it('keeps the places non-negative', () => {
    for (const [, spec] of CASES) {
      const graph = build(spec);
      for (const [direction, compact] of MODES) {
        const positions = layoutGraph(graph, direction, undefined, compact);
        for (const at of Object.values(positions)) {
          expect(at.x).toBeGreaterThanOrEqual(0);
          expect(at.y).toBeGreaterThanOrEqual(0);
        }
      }
    }
  });

  // The gap between two cards of a chain is the gap of plain steps, whatever
  // the height of the columns of the step in the middle.
  it.each(MODES)(
    'gives a chain the gap of plain steps (%s, compact %s)',
    (direction, compact) => {
      const gap = direction === 'LR' ? 96 : compact ? 64 : 72;
      const horizontal = direction === 'LR';
      for (const middle of [FULL, MODEL_ONLY, BARE]) {
        const graph = build([
          plain('a'),
          agent('b', ['a'], middle as typeof FULL),
          plain('c', 'b'),
        ]);
        const [a, b, c] = ['steps[0]', 'steps[1]', 'steps[2]'].map(
          (key) => rects(graph, direction, compact).find((r) => r.key === key)!
        );
        const end = (r: NonNullable<typeof a>) =>
          horizontal ? r.x + r.width : r.y + r.height;
        const start = (r: NonNullable<typeof a>) => (horizontal ? r.x : r.y);
        expect(start(b!) - end(a!)).toBe(gap);
        expect(start(c!) - end(b!)).toBe(gap);
        // The edges run straight: no point bends them.
        expect(
          layoutRoutes(graph, direction, undefined, compact).waypoints
        ).toEqual({});
      }
    }
  );

  // Two steps with sub-nodes follow each other: their columns overlap in the
  // flow, so the gap grows until they do not.
  it.each(MODES)(
    'grows the gap between two steps whose sub-nodes meet (%s, compact %s)',
    (direction, compact) => {
      const graph = build([agent('a', []), agent('b', ['a'])]);
      const all = rects(graph, direction, compact);
      const horizontal = direction === 'LR';
      const a = all.find((r) => r.key === 'steps[0]')!;
      const b = all.find((r) => r.key === 'steps[1]')!;
      const gap = horizontal ? b.x - (a.x + a.width) : b.y - (a.y + a.height);
      expect(gap).toBeGreaterThan(direction === 'LR' ? 96 : compact ? 64 : 72);
    }
  );

  it('keeps the box of a step that a bent edge leaves, on the side of the edge', () => {
    const graph = build([
      plain('s'),
      agent('x', ['s']),
      agent('y', ['s']),
      plain('j', 'x', 'y'),
    ]);
    // The fan bends its edges: the box holds the sub-nodes, as before.
    for (const direction of ['TB', 'LR'] as const) {
      const node = graph.nodes.find((n) => n.key === 'steps[1]')!;
      const plan = chatPlan(node, false, direction)!;
      const positions = layoutGraph(graph, direction);
      const next = positions['steps[3]']!;
      const at = positions['steps[1]']!;
      const along = direction === 'LR' ? 'x' : 'y';
      const size = direction === 'LR' ? plan.width : plan.height;
      expect(next[along] - at[along]).toBeGreaterThanOrEqual(size);
    }
  });
});

function def(over: Partial<ToolDefinition>): ToolDefinition {
  return { name: 'x', ...over };
}

describe('entryMark', () => {
  const entries = readStepTools(CHAT).entries;

  it('is unknown without a catalog', () => {
    expect(entryMark(entries[0]!, undefined)).toEqual({ state: 'unknown' });
  });

  it('marks a DAG tool by its source', () => {
    const defs = [def({ name: 'lookup', source: 'dag:lookup' })];
    expect(entryMark(entries[0]!, defs)).toEqual({ state: 'offered' });
    expect(entryMark(entries[1]!, defs)).toEqual({ state: 'unknown' });
  });

  it('marks an MCP server by its name', () => {
    const defs = [
      def({ name: 'lookup', source: 'dag:lookup' }),
      def({ name: 'docs_search', source: 'mcp:docs' }),
    ];
    expect(entryMark(entries[2]!, defs)).toEqual({ state: 'offered' });
    expect(entryMark(entries[1]!, defs)).toEqual({ state: 'unknown' });
  });

  it('falls back to the tool name when a record has no source', () => {
    const dag = readStepTools({
      with: { dags: [{ name: 'ops.notify' }] },
    }).entries[0]!;
    expect(entryMark(dag, [def({ name: 'ops_notify' })])).toEqual({
      state: 'offered',
    });
  });
});

describe('skill names', () => {
  it('reads the names only: an item that is not a name is left out', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: {
          action: 'chat.completion',
          with: {
            skills: ['plain', { skill: 'mapped', inline: true }, 'other'],
          },
        },
      }),
    ]);
    const base = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
    const card = withToolSatellites(base).nodes.find(
      (node) => node.key === slotCardKey('steps[0]', 'skills')
    ) as CanvasNodeWithRoutes;
    expect(card.slotCard!.skills).toEqual([
      { name: 'plain', index: 0 },
      { name: 'other', index: 2 },
    ]);
  });
});

describe('the width of the Tools card', () => {
  // A step with MCP servers and built-in tools has two kinds: the card goes in
  // columns above eight tools. The DAG tools have their own card.
  const tools = (m: number, builtin: Record<string, unknown>) => ({
    mcp: Array.from({ length: m }, (_, i) => ({
      name: `m${i}`,
      url: `https://x${i}`,
      allow: [],
    })),
    builtin,
  });
  const draw = (value: unknown) => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        explicitName: 'triage',
        kind: 'action',
        action: 'agent.run',
        value: {
          action: 'agent.run',
          with: {
            provider: 'claude',
            prompt: 'x',
            dags: [{ name: 'ignored' }],
            tools: value,
          },
        },
      }),
    ]);
    const graph = withToolSatellites(
      buildCanvasGraph({
        model,
        names: makeNames(model),
        issues: { byStep: {}, dag: [] },
      })
    );
    return graph.nodes.find(
      (node) => (node as CanvasNodeWithRoutes).slotCard?.slot === 'tools'
    ) as CanvasNodeWithRoutes;
  };

  it('has one column up to eight tools, and one for each share of kinds above', () => {
    const one = draw(tools(3, { read: true, edit: true }));
    expect(one.slotCard?.cols).toBeUndefined();
    expect(nodeSize(one, false, 'TB').width).toBe(220);
    const many = draw(tools(12, { read: true, edit: true, search: true }));
    expect(many.slotCard?.cols).toBe(2);
    // Heading, eight tools, "+N more", the add row, and the limits row.
    expect(many.slotCard?.rows).toBe(12);
    expect(nodeSize(many, false, 'TB').width).toBe(440);
    // The DAG tool is not in the card.
    expect(many.slotCard?.entries.every((entry) => entry.kind === 'mcp')).toBe(
      true
    );
  });
});
