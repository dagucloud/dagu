// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { buildCanvasGraph } from '../graph';
import {
  addSatelliteKey,
  modelSatelliteKey,
  withToolSatellites,
} from '../toolSatellites';
import type { CanvasNodeWithRoutes } from '../types';
import { graphShapeKey } from '../layout';
import { MODEL_CARD_WIDTH, modelCardHeight, subNodeSize } from '../subRow';
import { makeModel, makeNames, makeStep } from './fixtures';

// The Model node has the size of its card of controls in an edit view, and the
// size of the round node in a run view and in a compact canvas.

function drawn(
  action: string,
  withValue: Record<string, unknown>,
  controls: boolean
) {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      explicitName: 'code',
      kind: 'action',
      action,
      value: { action, with: withValue },
    }),
  ]);
  const graph = withToolSatellites(
    buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    }),
    true,
    controls
  );
  const owner = graph.nodes.find(
    (node) => node.key === 'steps[0]'
  ) as CanvasNodeWithRoutes;
  const sat = graph.nodes.find(
    (node) => node.key === modelSatelliteKey('steps[0]')
  ) as CanvasNodeWithRoutes | undefined;
  return { owner, sat, graph };
}

describe('withToolSatellites and the card of the Model node', () => {
  it('gives the card to an agent step, with a line for its CLI, in the shape of the step and in the node', () => {
    const { owner, sat } = drawn(
      'agent.run',
      { provider: 'claude', endpoint: { name: 'cle1', model: 'x' } },
      true
    );
    expect(sat?.modelSat?.card).toEqual({
      rows: 1,
      cli: true,
      effort: true,
    });
    expect(sat?.modelSat?.edit?.kind).toBe('agent');
    expect(
      owner.subRow?.items.find((item) => item.slot === 'model')?.modelCard
    ).toEqual({
      rows: 1,
      cli: true,
      effort: true,
    });
    expect(subNodeSize(sat!, false)).toEqual({
      width: MODEL_CARD_WIDTH,
      height: modelCardHeight(1, true, true),
    });
  });

  it('gives a card of a block for each model to a chat step with a list', () => {
    const { sat } = drawn(
      'chat.completion',
      { model: [{ name: 'a' }, { name: 'b' }, { name: 'c' }] },
      true
    );
    expect(sat?.modelSat?.card).toEqual({
      rows: 3,
      cli: false,
      effort: false,
    });
  });

  // The layout runs again only when the shape key changes: a card that grows
  // or shrinks must change it, or it overlaps the sub-node under it.
  it('changes the shape key of the graph when the card changes its size', () => {
    const key = (action: string, withValue: Record<string, unknown>) =>
      graphShapeKey(drawn(action, withValue, true).graph, 'TB');
    const one = key('chat.completion', {
      model: [{ name: 'a', provider: 'openai' }],
    });
    expect(
      key('chat.completion', {
        model: [
          { name: 'a', provider: 'openai' },
          { name: 'b', provider: 'openai' },
        ],
      })
    ).not.toBe(one);
    // The old method has no Effort control; the block has one.
    expect(key('chat.completion', { provider: 'openai', model: 'm' })).not.toBe(
      key('chat.completion', { endpoint: { name: 'e', model: 'm' } })
    );
    // The CLI line of an agent step.
    expect(key('agent.run', { prompt: 'x' })).not.toBe(
      key('agent.run', { provider: 'claude', prompt: 'x' })
    );
    // A new value of a control keeps the size, and the key.
    expect(key('agent.run', { endpoint: { name: 'a', model: 'm' } })).toBe(
      key('agent.run', { endpoint: { name: 'b', model: 'n' } })
    );
  });

  it('gives no card in a run view, and the round node keeps its size', () => {
    const { owner, sat } = drawn(
      'agent.run',
      { provider: 'claude', endpoint: { model: 'x' } },
      false
    );
    expect(sat?.modelSat?.card).toBeUndefined();
    expect(
      owner.subRow?.items.find((item) => item.slot === 'model')?.modelCard
    ).toBeUndefined();
    expect(subNodeSize(sat!, false).width).toBeLessThan(MODEL_CARD_WIDTH);
  });

  it('gives the round node to a compact canvas, even with a card', () => {
    const { sat } = drawn(
      'agent.run',
      { provider: 'claude', endpoint: { model: 'x' } },
      true
    );
    expect(subNodeSize(sat!, true).width).toBeLessThan(MODEL_CARD_WIDTH);
  });

  it('gives the card to a step with no model in the edit view, in place of the dashed "+"', () => {
    const { owner, sat } = drawn('chat.completion', {}, true);
    const card = { rows: 1, cli: false, effort: true };
    expect(sat?.modelSat?.models).toEqual([]);
    expect(sat?.modelSat?.card).toEqual(card);
    expect(
      owner.subRow?.items.find((item) => item.slot === 'model')
    ).toMatchObject({ key: modelSatelliteKey('steps[0]'), modelCard: card });
    expect(subNodeSize(sat!, false)).toEqual({
      width: MODEL_CARD_WIDTH,
      height: modelCardHeight(1, false, true),
    });
  });

  it('gives an agent step with no model the card and its Effort control', () => {
    const { sat } = drawn('agent.run', { prompt: 'x' }, true);
    expect(sat?.modelSat?.models).toEqual([]);
    expect(sat?.modelSat?.card).toEqual({ rows: 1, cli: false, effort: true });
    expect(subNodeSize(sat!, false).height).toBe(
      modelCardHeight(1, false, true)
    );
  });

  it('keeps the dashed "+" of a step with no model in a canvas with no controls', () => {
    for (const action of ['chat.completion', 'agent.run']) {
      const { owner, sat } = drawn(action, {}, false);
      expect(sat).toBeUndefined();
      const add = owner.subRow?.items.find((item) => item.slot === 'model');
      expect(add).toEqual({
        key: addSatelliteKey('steps[0]'),
        slot: 'model',
      });
    }
  });

  it('keeps the dashed "+" while a list of models has no row to fill', () => {
    const { owner, sat } = drawn('chat.completion', { model: [] }, true);
    expect(sat).toBeUndefined();
    expect(owner.subRow?.items[0]).toEqual({
      key: addSatelliteKey('steps[0]'),
      slot: 'model',
    });
  });
});
