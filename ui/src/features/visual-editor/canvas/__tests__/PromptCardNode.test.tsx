// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CanvasActionsContext, type CanvasActions } from '../context';
import { FlowProvider, type NodeProps } from '../engine';
import { AddSubNode } from '../nodes/AddSubNode';
import { PromptCardNode } from '../nodes/PromptCardNode';
import {
  PROMPT_PREVIEW_CHARS,
  SYSTEM_PREVIEW_CHARS,
  previewText,
  promptFit,
  previewLines,
  PROMPT_FULL,
} from '../promptCard';
import { promptCardHeight } from '../subRow';
import type { CanvasNodeWithRoutes, StepFlowNode } from '../types';

describe('promptFit', () => {
  it('gives a short card for short texts and the full card for long ones', () => {
    expect(promptFit('', '')).toEqual({ system: 1, prompt: 2 });
    expect(promptFit('Be brief.', 'Fix the test.')).toEqual({
      system: 1,
      prompt: 2,
    });
    const long = 'Fix the failing test of the calculator. '.repeat(10);
    expect(promptFit(long, long)).toEqual(PROMPT_FULL);
  });

  it('counts a paragraph, a list item and a wide character as the card shows them', () => {
    // The lines of a paragraph join, so a hard wrap does not add a line.
    expect(previewLines('one\ntwo\nthree', 238, 11)).toBe(1);
    // A blank line starts a paragraph, and each item of a list is a line.
    expect(previewLines('one\n\ntwo', 238, 11)).toBe(2);
    expect(previewLines('- a\n- b\n- c', 238, 11)).toBe(3);
    // A wide character is a square of the font: 30 of them are two lines.
    expect(previewLines('修'.repeat(30), 238, 11)).toBe(2);
    expect(previewLines('', 238, 11)).toBe(0);
  });
});

describe('previewText', () => {
  it('keeps a short text and trims it', () => {
    expect(previewText('  hello\n', 50)).toBe('hello');
  });

  it('cuts a long text at the character limit and says so', () => {
    const cut = previewText('a'.repeat(500), PROMPT_PREVIEW_CHARS);
    expect(cut).toBe(`${'a'.repeat(PROMPT_PREVIEW_CHARS)}…`);
    expect(previewText('b'.repeat(200), SYSTEM_PREVIEW_CHARS).length).toBe(
      SYSTEM_PREVIEW_CHARS + 1
    );
  });

  it('keeps at most eight lines', () => {
    const lines = Array.from({ length: 12 }, (_, i) => `line ${i}`).join('\n');
    const cut = previewText(lines, 1000);
    expect(cut.split('\n')).toHaveLength(8);
    expect(cut.endsWith('…')).toBe(true);
  });
});

function actions(over: Partial<CanvasActions> = {}): CanvasActions {
  return {
    direction: 'TB',
    editable: true,
    onCommand: () => {},
    onDeleteStep: () => {},
    onInsertOnEdge: () => {},
    ...over,
  };
}

function cardNode(system: string, prompt: string): CanvasNodeWithRoutes {
  return {
    key: 'toolsat:steps[0]:prompt',
    label: 'Prompt',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    promptCard: { owner: 'steps[0]', system, prompt, editable: true },
  };
}

function props(node: CanvasNodeWithRoutes): NodeProps<StepFlowNode> {
  return {
    id: node.key,
    data: { node },
    selected: false,
    isConnectable: false,
  };
}

function view(ui: React.ReactElement, over: Partial<CanvasActions> = {}) {
  return render(
    <FlowProvider>
      <CanvasActionsContext.Provider value={actions(over)}>
        {ui}
      </CanvasActionsContext.Provider>
    </FlowProvider>
  );
}

describe('the Prompt card', () => {
  it('renders the first characters of both texts as Markdown, never the raw marks', () => {
    const long = `# Title\n\n**bold** ${'x'.repeat(1000)}`;
    const { getByTestId } = view(
      <PromptCardNode {...props(cardNode('Be *brief*.', long))} />
    );
    const system = getByTestId('ve-canvas-prompt-system');
    expect(system.querySelector('em')).toHaveTextContent('brief');
    const prompt = getByTestId('ve-canvas-prompt-text');
    expect(prompt.querySelector('h1')).toHaveTextContent('Title');
    expect(prompt.querySelector('strong')).toHaveTextContent('bold');
    expect(prompt).not.toHaveTextContent('**');
    // The text is cut: the card never holds the whole prompt.
    expect(prompt.textContent!.length).toBeLessThan(PROMPT_PREVIEW_CHARS + 60);
    expect(prompt).toHaveTextContent('…');
  });

  it('has the height of the layout for its texts, cut or not', () => {
    const { getByTestId } = view(
      <PromptCardNode {...props(cardNode('', 'short'))} />
    );
    // The blocks reserve their height: the parts add up to the height of the card.
    const card = getByTestId('ve-canvas-node-toolsat:steps[0]:prompt');
    const sum = Array.from(card.querySelectorAll<HTMLElement>('[style]'))
      .map((el) => Number.parseFloat(el.style.height))
      .reduce((total, h) => total + (Number.isNaN(h) ? 0 : h), 0);
    // Header, two labels, two previews, plus the padding of the card.
    expect(sum).toBe(promptCardHeight(promptFit('', 'short')) - 8);
  });

  it('marks an empty text, and opens the dock from a click, Enter or Space', () => {
    const onCommand = vi.fn();
    const { getByTestId, getByRole } = view(
      <PromptCardNode {...props(cardNode('', 'x'))} />,
      { onCommand }
    );
    expect(getByTestId('ve-canvas-prompt-system')).toHaveAttribute(
      'data-empty'
    );
    expect(getByTestId('ve-canvas-prompt-text')).not.toHaveAttribute(
      'data-empty'
    );
    const card = getByRole('button', {
      name: 'Edit the prompt and the system message',
    });
    fireEvent.click(card);
    fireEvent.keyDown(card, { key: 'Enter' });
    fireEvent.keyDown(card, { key: ' ' });
    expect(onCommand).toHaveBeenCalledTimes(3);
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openPromptDock',
      step: 'steps[0]',
    });
  });
});

describe('the focus ring of the Prompt card', () => {
  // The ring is on the card itself, as on the other cards.
  it('draws its own ring for the keyboard focus', () => {
    const { getByRole } = view(
      <PromptCardNode {...props(cardNode('', 'x'))} />
    );
    const card = getByRole('button', {
      name: 'Edit the prompt and the system message',
    });
    expect(card.className).toMatch(/(^|\s)focus-visible:ring-2(\s|$)/);
  });
});

describe('the "+" of an empty Prompt slot', () => {
  const plus: CanvasNodeWithRoutes = {
    key: 'toolsat:steps[0]:add-prompt',
    label: '',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    addSat: { owner: 'steps[0]', slot: 'prompt' },
  };

  it('opens the dock of the prompt, and is dashed and read-only on a canvas that only reads', () => {
    const onCommand = vi.fn();
    const editable = view(<AddSubNode {...props(plus)} />, { onCommand });
    fireEvent.click(editable.getByRole('button', { name: 'Add a prompt' }));
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openPromptDock',
      step: 'steps[0]',
    });
    editable.unmount();
    const readOnly = view(<AddSubNode {...props(plus)} />, { editable: false });
    expect(readOnly.queryByRole('button')).toBeNull();
    expect(
      readOnly.getByRole('img', { name: 'No prompt' })
    ).toBeInTheDocument();
  });
});
