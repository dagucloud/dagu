// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type {
  CanvasDirection,
  CanvasGraph,
  VisualCanvasProps,
} from '../../contracts';
import { catalogSample } from '../../testing/fixtures';
import { buildCanvasGraph } from '../graph';
import { cardPositions, chatPlan } from '../layout';
import { facingSide, movedSubLinks, NO_SUB_LINKS } from '../subLinks';
import {
  addSatelliteKey,
  addSlotKey,
  modelSatelliteKey,
  promptCardKey,
  slotCardKey,
  withFollowers,
  withToolSatellites,
} from '../toolSatellites';
import type { NodePositions } from '../types';
import { CANVAS_DETAILS_KEY } from '../useCanvasDetails';
import { useCanvasLayout } from '../useCanvasLayout';
import { VisualCanvas } from '../VisualCanvas';
import { makeModel, makeNames, makeStep } from './fixtures';

const OWNER = 'steps[0]';
const MODEL = modelSatelliteKey(OWNER);
const PROMPT = promptCardKey(OWNER);
const SKILLS = slotCardKey(OWNER, 'skills');
const TOOLS = slotCardKey(OWNER, 'tools');
const SUBS = [MODEL, PROMPT, SKILLS, TOOLS];

// An agent step with the four sub-nodes: model, prompt, skills and tools.
function agentGraph(): CanvasGraph {
  const model = makeModel([
    makeStep({
      key: OWNER,
      explicitName: 'code',
      kind: 'action',
      action: 'agent.run',
      value: {
        action: 'agent.run',
        with: {
          provider: 'claude',
          endpoint: { model: 'claude-haiku-4-5' },
          prompt: 'fix it',
          tools: { builtin: { read: true, edit: true } },
          skills: ['review'],
        },
      },
    }),
  ]);
  return buildCanvasGraph({
    model,
    names: makeNames(model),
    issues: { byStep: {}, dag: [] },
  });
}

afterEach(() => {
  window.localStorage.removeItem(CANVAS_DETAILS_KEY);
});

describe('withFollowers', () => {
  const drawn = withToolSatellites(agentGraph());
  const home: NodePositions = {
    [OWNER]: { x: 300, y: 100 },
    [MODEL]: { x: 0, y: 40 },
    [PROMPT]: { x: 0, y: 200 },
    [SKILLS]: { x: 700, y: 40 },
    [TOOLS]: { x: 700, y: 200 },
  };

  it('moves a sub-node alone: the step and the other sub-nodes stay', () => {
    expect(withFollowers(drawn, home, { [TOOLS]: { x: 900, y: 500 } })).toEqual(
      { [TOOLS]: { x: 900, y: 500 } }
    );
  });

  it('carries the sub-nodes with their step, with the offsets that they have', () => {
    const offset = { ...home, [MODEL]: { x: -50, y: 300 } };
    const moved = withFollowers(drawn, offset, {
      [OWNER]: { x: 320, y: 90 },
    });
    expect(moved[OWNER]).toEqual({ x: 320, y: 90 });
    // A sub-node that was moved alone keeps its offset to the step.
    expect(moved[MODEL]).toEqual({ x: -30, y: 290 });
    expect(moved[TOOLS]).toEqual({ x: 720, y: 190 });
  });

  it('lets a sub-node that the drag names keep its own place', () => {
    const moved = withFollowers(drawn, home, {
      [OWNER]: { x: 320, y: 90 },
      [TOOLS]: { x: 5, y: 6 },
    });
    expect(moved[TOOLS]).toEqual({ x: 5, y: 6 });
    expect(moved[MODEL]).toEqual({ x: 20, y: 30 });
  });
});

describe('the places of the sub-nodes in the layout', () => {
  const drawn = withToolSatellites(agentGraph());

  function layout(direction: CanvasDirection, compact = false) {
    return renderHook(
      (props: { direction: CanvasDirection; compact: boolean }) =>
        useCanvasLayout(drawn, props.direction, undefined, props.compact),
      { initialProps: { direction, compact } }
    );
  }
  function drag(hook: ReturnType<typeof layout>, moved: NodePositions) {
    act(() =>
      hook.result.current.moveNodes(
        withFollowers(drawn, hook.result.current.positions, moved)
      )
    );
  }

  it.each(['TB', 'LR'] as const)(
    'keeps the step and the other sub-nodes where they are when one sub-node moves, %s',
    (direction) => {
      const hook = layout(direction);
      const before = hook.result.current.positions;
      drag(hook, { [MODEL]: { x: before[MODEL]!.x + 140, y: 15 } });
      const after = hook.result.current.positions;
      expect(after[MODEL]).toEqual({ x: before[MODEL]!.x + 140, y: 15 });
      for (const key of [OWNER, PROMPT, SKILLS, TOOLS]) {
        expect(after[key]).toEqual(before[key]);
      }
    }
  );

  it.each(['TB', 'LR'] as const)(
    'carries the sub-nodes with the step, and keeps the offset of a sub-node moved alone, %s',
    (direction) => {
      const hook = layout(direction);
      const home = hook.result.current.positions;
      drag(hook, {
        [TOOLS]: { x: home[TOOLS]!.x + 60, y: home[TOOLS]!.y - 70 },
      });
      const moved = hook.result.current.positions;
      drag(hook, {
        [OWNER]: { x: home[OWNER]!.x + 25, y: home[OWNER]!.y + 40 },
      });
      const after = hook.result.current.positions;
      for (const key of [OWNER, ...SUBS]) {
        expect(after[key]).toEqual({
          x: moved[key]!.x + 25,
          y: moved[key]!.y + 40,
        });
      }
      // The offset of the Tools card to the step is the one that the user gave.
      expect(after[TOOLS]!.x - after[OWNER]!.x).toBe(
        home[TOOLS]!.x + 60 - home[OWNER]!.x
      );
    }
  );

  it('forgets the moves when the layout runs again: a new direction, the compact flag, the align button', () => {
    const hook = layout('TB');
    const home = hook.result.current.positions;
    drag(hook, { [MODEL]: { x: 9, y: 9 } });
    expect(hook.result.current.positions[MODEL]).toEqual({ x: 9, y: 9 });
    act(() => hook.result.current.realign());
    expect(hook.result.current.positions).toEqual(home);

    drag(hook, { [MODEL]: { x: 9, y: 9 } });
    hook.rerender({ direction: 'LR', compact: false });
    expect(hook.result.current.positions[MODEL]).not.toEqual({ x: 9, y: 9 });

    drag(hook, { [MODEL]: { x: 9, y: 9 } });
    hook.rerender({ direction: 'LR', compact: true });
    expect(hook.result.current.positions[MODEL]).not.toEqual({ x: 9, y: 9 });
  });

  it('draws the steps of an export where the step is, whatever the sub-nodes do', () => {
    const hook = layout('TB');
    const home = cardPositions(
      drawn,
      hook.result.current.positions,
      false,
      'TB'
    );
    drag(hook, { [MODEL]: { x: 9, y: 9 } });
    expect(
      cardPositions(drawn, hook.result.current.positions, false, 'TB')
    ).toMatchObject({ [OWNER]: home[OWNER] });
  });
});

describe('the entry of a moved sub-node', () => {
  const drawn = withToolSatellites(agentGraph());

  function links(direction: CanvasDirection, compact: boolean) {
    const hook = renderHook(() =>
      useCanvasLayout(drawn, direction, undefined, compact)
    );
    const owner = drawn.nodes.find((node) => node.key === OWNER)!;
    const plan = chatPlan(owner, compact, direction)!;
    const home = hook.result.current.positions;
    const move = (key: string, dx: number, dy: number) =>
      movedSubLinks(
        drawn,
        { ...home, [key]: { x: home[key]!.x + dx, y: home[key]!.y + dy } },
        compact,
        direction
      );
    return { plan, home, move };
  }

  it.each([
    ['TB', false],
    ['LR', false],
    ['TB', true],
    ['LR', true],
  ] as const)(
    'has no change while no sub-node moved, %s compact %s',
    (direction, compact) => {
      const { home } = links(direction, compact);
      // The same value each time: the sub-nodes do not draw again in a drag.
      expect(movedSubLinks(drawn, home, compact, direction)).toBe(NO_SUB_LINKS);
    }
  );

  it('keeps the side of the slot while the sub-node stays on it, top to bottom', () => {
    const { move } = links('TB', false);
    // The Tools card moves down on its side: its entry still faces the step from the right.
    const result = move(TOOLS, 30, 400);
    expect(result.sides[TOOLS]).toBe('right');
    expect(result.sides[MODEL]).toBeUndefined();
  });

  it('turns the entry to the side that faces a sub-node that crossed the step', () => {
    const { move, plan } = links('TB', false);
    expect(move(MODEL, plan.width, 0).sides[MODEL]).toBe('right');
  });

  it('puts the entry of a sub-node above or under the step on the upper or the lower side', () => {
    const { move, plan } = links('TB', false);
    // The sub-node is over the columns of the step, wholly above or under it.
    const tools = plan.items[TOOLS]!;
    const above = move(
      TOOLS,
      plan.base.x + 10 - tools.x,
      plan.base.y - 40 - tools.height - tools.y
    );
    expect(above.sides[TOOLS]).toBe('top');
    const model = plan.items[MODEL]!;
    const under = move(
      MODEL,
      plan.base.x + 10 - model.x,
      plan.base.y + plan.base.height + 40 - model.y
    );
    expect(under.sides[MODEL]).toBe('bottom');
  });

  it('faces the left side for a sub-node far left of the step, left to right', () => {
    const lr = links('LR', false);
    const model = lr.plan.items[MODEL]!;
    const left = lr.move(MODEL, -900, lr.plan.card.y + 5 - model.y);
    expect(left.sides[MODEL]).toBe('left');
  });

  it('keeps the side of the slot for a sub-node over the step', () => {
    const { plan } = links('TB', false);
    const over = { ...plan.card, width: 40, height: 40 };
    expect(facingSide(plan, over, 'left')).toBe('left');
  });

  it('chooses, for a sub-node on a diagonal, the side with the wider gap', () => {
    const { plan } = links('TB', false);
    const base = plan.base;
    const box = {
      x: base.x + base.width + 400,
      y: base.y + base.height + 40,
      width: 100,
      height: 50,
    };
    expect(facingSide(plan, box, 'left')).toBe('right');
  });

  it('moves the "+" of an empty slot like a card', () => {
    const model = makeModel([
      makeStep({
        key: OWNER,
        kind: 'action',
        action: 'chat.completion',
        value: {
          action: 'chat.completion',
          with: { provider: 'openai', model: 'gpt-4o' },
        },
      }),
    ]);
    const bare = withToolSatellites(
      buildCanvasGraph({
        model,
        names: makeNames(model),
        issues: { byStep: {}, dag: [] },
      })
    );
    const hook = renderHook(() => useCanvasLayout(bare, 'TB'));
    const home = hook.result.current.positions;
    const key = addSlotKey(OWNER, 'tools');
    const plan = chatPlan(bare.nodes[0]!, false, 'TB')!;
    const result = movedSubLinks(
      bare,
      {
        ...home,
        [key]: { x: home[key]!.x - plan.width - 200, y: home[key]!.y },
      },
      false,
      'TB'
    );
    expect(result.sides[key]).toBe('left');
  });
});

// The canvas in a DOM: a drag with the pointer, as for the other nodes.
function pointer(
  target: Element | Window,
  type: 'pointerdown' | 'pointermove' | 'pointerup',
  x: number,
  y: number
) {
  act(() => {
    target.dispatchEvent(
      new MouseEvent(type, {
        bubbles: true,
        cancelable: true,
        clientX: x,
        clientY: y,
        button: 0,
      })
    );
  });
}

function drop(target: Element, dx: number, dy: number) {
  pointer(target, 'pointerdown', 100, 100);
  pointer(window, 'pointermove', 100 + dx, 100 + dy);
  pointer(window, 'pointerup', 100 + dx, 100 + dy);
}

function nodeCard(key: string): HTMLElement {
  return screen.getByTestId(`ve-canvas-node-${key}`);
}

function wrapperOf(key: string): HTMLElement {
  return nodeCard(key).closest<HTMLElement>('[data-flow-node]')!;
}

function placeOf(key: string): { x: number; y: number } {
  const match = /translate\((-?[\d.]+)px, (-?[\d.]+)px\)/.exec(
    wrapperOf(key).style.transform
  );
  if (!match) throw new Error(`no place for ${key}`);
  return { x: Number(match[1]), y: Number(match[2]) };
}

function placesOf(): Record<string, { x: number; y: number }> {
  return Object.fromEntries([OWNER, ...SUBS].map((key) => [key, placeOf(key)]));
}

function renderCanvas(overrides: Partial<VisualCanvasProps> = {}) {
  const props: VisualCanvasProps = {
    graph: agentGraph(),
    direction: 'TB',
    selected: null,
    frozen: false,
    readOnly: false,
    catalog: catalogSample,
    onSelect: vi.fn(),
    onEdit: vi.fn(),
    onCommand: vi.fn(),
    onDirectionChange: vi.fn(),
    ...overrides,
  };
  const view = render(
    <UserPreferencesProvider>
      <VisualCanvas {...props} />
    </UserPreferencesProvider>
  );
  return { ...view, props };
}

function slotLabel(slot: string): HTMLElement {
  return screen.getByTestId(`ve-canvas-slot-${slot}`);
}

describe('a drag of a sub-node in the canvas', () => {
  it('moves the sub-node alone, and leaves the step and the other sub-nodes', () => {
    renderCanvas();
    const before = placesOf();
    drop(wrapperOf(TOOLS), 80, 60);
    const after = placesOf();
    expect(after[TOOLS]).not.toEqual(before[TOOLS]);
    for (const key of [OWNER, MODEL, PROMPT, SKILLS]) {
      expect(after[key]).toEqual(before[key]);
    }
  });

  it('carries the sub-nodes with their step, with the offset of the one moved alone', () => {
    renderCanvas();
    drop(wrapperOf(TOOLS), 80, 60);
    const before = placesOf();
    drop(wrapperOf(OWNER).querySelector('[data-testid]')!, 50, 30);
    const after = placesOf();
    const dx = after[OWNER]!.x - before[OWNER]!.x;
    const dy = after[OWNER]!.y - before[OWNER]!.y;
    expect(dx).not.toBe(0);
    for (const key of SUBS) {
      expect(after[key]).toEqual({
        x: before[key]!.x + dx,
        y: before[key]!.y + dy,
      });
    }
  });

  it('keeps the connector of the step fixed when the sub-node crosses to the other side', () => {
    renderCanvas();
    expect(slotLabel('model')).toHaveAttribute('data-side', 'left');
    // The Model card goes over the step to its right side.
    drop(wrapperOf(MODEL), 1500, 0);
    // The connector on the step stays where it is: only the entry of the Model turns.
    expect(slotLabel('model')).toHaveAttribute('data-side', 'left');
    const entry = wrapperOf(MODEL).querySelector('[data-handle-id="target"]');
    expect(entry).toHaveAttribute('data-handle-position', 'left');
    expect(slotLabel('tools')).toHaveAttribute('data-side', 'right');
  });

  it('starts again from the layout when the direction changes', () => {
    const { rerender, props } = renderCanvas();
    const home = placesOf();
    drop(wrapperOf(MODEL), 90, 70);
    expect(placeOf(MODEL)).not.toEqual(home[MODEL]);
    rerender(
      <UserPreferencesProvider>
        <VisualCanvas {...props} direction="LR" />
      </UserPreferencesProvider>
    );
    const lr = placesOf();
    rerender(
      <UserPreferencesProvider>
        <VisualCanvas {...props} direction="TB" />
      </UserPreferencesProvider>
    );
    expect(placesOf()).toEqual(home);
    expect(lr[MODEL]).not.toEqual(home[MODEL]);
  });

  it('moves the sub-nodes of a compact canvas too', () => {
    window.localStorage.setItem(CANVAS_DETAILS_KEY, '0');
    renderCanvas();
    const before = placesOf();
    drop(wrapperOf(MODEL), 60, 40);
    expect(placeOf(MODEL)).not.toEqual(before[MODEL]);
    expect(placeOf(OWNER)).toEqual(before[OWNER]);
  });

  it('moves a sub-node of a run view, as the other nodes', () => {
    renderCanvas({ readOnly: true });
    const before = placeOf(TOOLS);
    drop(wrapperOf(TOOLS), 80, 60);
    expect(placeOf(TOOLS)).not.toEqual(before);
  });

  it('moves nothing on a frozen canvas', () => {
    renderCanvas({ frozen: true });
    const before = placesOf();
    drop(wrapperOf(TOOLS), 80, 60);
    drop(wrapperOf(MODEL), 80, 60);
    expect(placesOf()).toEqual(before);
  });

  it('does not take the end of a drag for a click', () => {
    const { props } = renderCanvas();
    const card = nodeCard(TOOLS);
    drop(wrapperOf(TOOLS), 80, 60);
    fireEvent.click(card);
    expect(props.onSelect).not.toHaveBeenCalled();
    expect(props.onCommand).not.toHaveBeenCalled();
    // A click with no drag still selects the step.
    fireEvent.click(card);
    expect(props.onSelect).toHaveBeenCalledWith(OWNER);
  });

  it('does not take the end of a drag for a click on a button of the card', () => {
    const { props } = renderCanvas();
    const button = within(nodeCard(SKILLS)).getAllByRole('button')[0]!;
    drop(wrapperOf(SKILLS), 80, 60);
    fireEvent.click(button);
    expect(props.onCommand).not.toHaveBeenCalled();
  });

  it('keeps the controls of the Model card out of a drag, and clickable', () => {
    renderCanvas();
    const before = placesOf();
    const field = within(nodeCard(MODEL)).getByRole('combobox', {
      name: 'Model',
    });
    drop(field, 80, 60);
    expect(placesOf()).toEqual(before);
    // The picker is a live control: a click opens its list.
    fireEvent.click(field);
    expect(field).toHaveAttribute('aria-expanded', 'true');
  });
});

describe('a drag of an empty slot "+" in the canvas', () => {
  // A chat step with a prompt only: the Skills and Tools slots are "+" nodes.
  // The Model slot is the card of pickers, or the dashed "+" of a compact canvas.
  function bareGraph(): CanvasGraph {
    const model = makeModel([
      makeStep({
        key: OWNER,
        explicitName: 'ask',
        kind: 'action',
        action: 'chat.completion',
        value: { action: 'chat.completion', with: { prompt: 'hi' } },
      }),
    ]);
    return buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    });
  }
  const ADD_MODEL = addSatelliteKey(OWNER);
  const ADD_TOOLS = addSlotKey(OWNER, 'tools');
  const ADD_SKILLS = addSlotKey(OWNER, 'skills');

  it.each([
    ['the round "+" of a slot', ADD_TOOLS, 'openSlotDock', false],
    [
      'the dashed "+" of the Model slot of a compact canvas',
      ADD_MODEL,
      'openForm',
      true,
    ],
  ])(
    'moves %s alone, and a click ends in no command',
    (_name, key, command, compact) => {
      if (compact) window.localStorage.setItem(CANVAS_DETAILS_KEY, '0');
      const { props } = renderCanvas({ graph: bareGraph() });
      const slots = compact ? [ADD_MODEL] : [MODEL];
      const rest = [OWNER, ...slots, ADD_TOOLS, ADD_SKILLS].filter(
        (other) => other !== key
      );
      const before = rest.map((other) => placeOf(other));
      const button = within(nodeCard(key)).getByTestId('ve-canvas-sub-add');
      const at = placeOf(key);
      drop(button, 70, 50);
      expect(placeOf(key)).not.toEqual(at);
      expect(rest.map((other) => placeOf(other))).toEqual(before);
      // The click that ends the drag adds nothing; a plain click still does.
      fireEvent.click(button);
      expect(props.onCommand).not.toHaveBeenCalled();
      fireEvent.click(button);
      expect(props.onCommand).toHaveBeenCalledWith(
        expect.objectContaining({ kind: command })
      );
    }
  );

  it('draws the pickers of the Model node, and no dashed "+", while the step names no model', () => {
    renderCanvas({ graph: bareGraph() });
    expect(screen.queryByTestId(`ve-canvas-node-${ADD_MODEL}`)).toBeNull();
    const card = nodeCard(MODEL);
    expect(card).toHaveAttribute('data-shape', 'model-card');
    // The Endpoint, the Model and the Effort pickers.
    expect(within(card).getAllByRole('combobox')).toHaveLength(3);
    expect(within(card).queryByTestId('ve-canvas-sub-add')).toBeNull();
  });

  it('moves the Model card of a step with no model alone, and keeps its pickers out of a drag', () => {
    renderCanvas({ graph: bareGraph() });
    const rest = [OWNER, ADD_TOOLS, ADD_SKILLS];
    const before = rest.map((other) => placeOf(other));
    const at = placeOf(MODEL);
    drop(wrapperOf(MODEL), 70, 50);
    expect(placeOf(MODEL)).not.toEqual(at);
    expect(rest.map((other) => placeOf(other))).toEqual(before);
    // A drag that starts on a picker moves nothing.
    const moved = placeOf(MODEL);
    drop(within(nodeCard(MODEL)).getAllByRole('combobox')[0]!, 70, 50);
    expect(placeOf(MODEL)).toEqual(moved);
  });

  it('keeps the dashed "+" of the Model slot in a run view, with no button', () => {
    renderCanvas({ graph: bareGraph(), readOnly: true });
    const add = nodeCard(ADD_MODEL);
    expect(add).toHaveAttribute('data-shape', 'add');
    expect(screen.queryByTestId(`ve-canvas-node-${MODEL}`)).toBeNull();
    expect(within(add).queryByTestId('ve-canvas-sub-add')).toBeNull();
  });
});
