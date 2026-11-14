// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { useRef, useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { duplicateStepsOp } from '../../doc/ops/clipboard';
import { parseDagDoc } from '../../doc/parse';
import { realSchema } from '../../inspector/__tests__/realSchema';
import { yamlArraySteps } from '../../testing/fixtures';
import { VisualEditor } from '../../VisualEditor';
import {
  CANVAS_KEY_TABLE,
  canvasKeyAction,
  canvasShortcut,
  isTextEntryTarget,
  useCanvasKeymap,
  type CanvasKeyHandlers,
} from '../useCanvasKeymap';

vi.mock(
  '@monaco-editor/react',
  async () =>
    (await import('../../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));
vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ data: undefined, isLoading: false, mutate: () => {} }),
  useClient: () => ({ POST: () => undefined }),
}));
vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
  }),
}));
vi.mock('@/contexts/AuthContext', () => ({ useCanExecute: () => true }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: realSchema }),
}));

function keyEvent(key: string, target: EventTarget | null = null) {
  return {
    key,
    target,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    defaultPrevented: false,
  };
}

function element(html: string, selector: string): Element {
  const root = document.createElement('div');
  root.innerHTML = html;
  document.body.appendChild(root);
  const found = root.querySelector(selector);
  if (!found) {
    throw new Error(`no element for ${selector}`);
  }
  return found;
}

describe('canvasKeyAction', () => {
  it.each([
    ['Delete', 'delete'],
    ['Backspace', 'delete'],
    ['Enter', 'open'],
    ['Escape', 'clear'],
    ['n', 'palette'],
    ['N', 'palette'],
    ['f', 'fit'],
    ['F', 'fit'],
    ['x', null],
  ])('maps %s', (key, action) => {
    expect(canvasKeyAction(keyEvent(key))).toBe(action);
  });

  it('ignores modifiers and handled events', () => {
    expect(canvasKeyAction({ ...keyEvent('n'), ctrlKey: true })).toBeNull();
    expect(canvasKeyAction({ ...keyEvent('n'), metaKey: true })).toBeNull();
    expect(canvasKeyAction({ ...keyEvent('f'), altKey: true })).toBeNull();
    expect(
      canvasKeyAction({ ...keyEvent('Delete'), defaultPrevented: true })
    ).toBeNull();
  });
});

describe('text entry guard', () => {
  it.each([
    ['<input />', 'input'],
    ['<textarea></textarea>', 'textarea'],
    ['<select></select>', 'select'],
    ['<div contenteditable="true"><span>x</span></div>', 'span'],
    ['<div contenteditable=""></div>', 'div'],
    [
      '<div class="monaco-editor"><div class="view-line"></div></div>',
      '.view-line',
    ],
  ])('ignores Delete in %s', (html, selector) => {
    const target = element(html, selector);
    expect(isTextEntryTarget(target)).toBe(true);
    expect(canvasKeyAction(keyEvent('Delete', target))).toBeNull();
  });

  it('leaves Enter to a focused button', () => {
    const target = element('<button><span>x</span></button>', 'span');
    expect(canvasKeyAction(keyEvent('Enter', target))).toBeNull();
    expect(canvasKeyAction(keyEvent('Delete', target))).toBe('delete');
  });

  it('keeps shortcuts on other elements', () => {
    const target = element(
      '<div class="visual-editor"><span>x</span></div>',
      'span'
    );
    expect(isTextEntryTarget(target)).toBe(false);
    expect(canvasKeyAction(keyEvent('Delete', target))).toBe('delete');
  });
});

function Harness(props: { handlers: CanvasKeyHandlers; enabled?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  useCanvasKeymap(ref, props.handlers, props.enabled);
  return (
    <div ref={ref} tabIndex={0} data-testid="canvas">
      <input aria-label="search" />
    </div>
  );
}

describe('useCanvasKeymap', () => {
  it('calls the handler and consumes the key', () => {
    const onDelete = vi.fn();
    const { getByTestId } = render(<Harness handlers={{ delete: onDelete }} />);
    const event = new KeyboardEvent('keydown', {
      key: 'Delete',
      bubbles: true,
      cancelable: true,
    });
    getByTestId('canvas').dispatchEvent(event);
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(event.defaultPrevented).toBe(true);
  });

  it('emits nothing for Delete in a focused input', () => {
    const onDelete = vi.fn();
    const { getByLabelText } = render(
      <Harness handlers={{ delete: onDelete }} />
    );
    const input = getByLabelText('search');
    input.focus();
    fireEvent.keyDown(input, { key: 'Delete' });
    expect(onDelete).not.toHaveBeenCalled();
  });

  it('uses the latest handlers and stops when disabled', () => {
    const first = vi.fn();
    const second = vi.fn();
    const { getByTestId, rerender } = render(
      <Harness handlers={{ fit: first }} />
    );
    rerender(<Harness handlers={{ fit: second }} />);
    fireEvent.keyDown(getByTestId('canvas'), { key: 'f' });
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);

    rerender(<Harness handlers={{ fit: second }} enabled={false} />);
    fireEvent.keyDown(getByTestId('canvas'), { key: 'f' });
    expect(second).toHaveBeenCalledTimes(1);
  });

  it('ignores keys outside the element', () => {
    const onPalette = vi.fn();
    render(<Harness handlers={{ palette: onPalette }} />);
    fireEvent.keyDown(document.body, { key: 'n' });
    expect(onPalette).not.toHaveBeenCalled();
  });
});

describe('menu, rename and help keys', () => {
  it.each([
    ['ContextMenu', false, 'menu'],
    ['F10', true, 'menu'],
    ['F10', false, null],
    ['F2', false, 'rename'],
    ['?', true, 'help'],
  ] as const)('maps %s (shift %s)', (key, shiftKey, action) => {
    expect(canvasKeyAction({ ...keyEvent(key), shiftKey })).toBe(action);
  });

  it('keeps them off in a text field and with Ctrl or Cmd', () => {
    const input = element('<input />', 'input');
    expect(canvasKeyAction(keyEvent('F2', input))).toBeNull();
    expect(canvasKeyAction({ ...keyEvent('?', input), shiftKey: true })).toBe(
      null
    );
    expect(canvasKeyAction({ ...keyEvent('F2'), ctrlKey: true })).toBeNull();
    expect(
      canvasKeyAction({ ...keyEvent('ContextMenu'), metaKey: true })
    ).toBeNull();
  });

  it('calls the menu handler from the canvas', () => {
    const menu = vi.fn();
    const { getByTestId } = render(<Harness handlers={{ menu }} />);
    fireEvent.keyDown(getByTestId('canvas'), { key: 'F10', shiftKey: true });
    fireEvent.keyDown(getByTestId('canvas'), { key: 'ContextMenu' });
    expect(menu).toHaveBeenCalledTimes(2);
  });
});

function modEvent(key: string, target: EventTarget | null = null) {
  return { ...keyEvent(key, target), ctrlKey: true, shiftKey: false };
}

function pasteOn(target: Element, text: string): boolean {
  return fireEvent.paste(target, {
    clipboardData: {
      getData: (type: string) => (type === 'text/plain' ? text : ''),
    },
  });
}

// parity:canvas-keymap parity:canvas-zoom-controls parity:run-graph-zoom-pan
describe('clipboard, zoom and arrow keys', () => {
  it.each([
    ['c', 'copy'],
    ['C', 'copy'],
    ['d', 'duplicate'],
    ['D', 'duplicate'],
    ['v', null],
    ['z', null],
  ] as const)('maps Ctrl/Cmd+%s', (key, action) => {
    expect(canvasKeyAction(modEvent(key))).toBe(action);
    expect(
      canvasKeyAction({ ...modEvent(key), ctrlKey: false, metaKey: true })
    ).toBe(action);
  });

  it('needs Ctrl or Cmd, and no Shift, for copy and duplicate', () => {
    expect(canvasKeyAction(keyEvent('c'))).toBeNull();
    expect(canvasKeyAction(keyEvent('d'))).toBeNull();
    expect(canvasKeyAction({ ...modEvent('C'), shiftKey: true })).toBeNull();
  });

  it.each([
    ['0', 'fit'],
    ['1', 'zoomReset'],
    ['+', 'zoomIn'],
    ['=', 'zoomIn'],
    ['-', 'zoomOut'],
    ['ArrowUp', 'navUp'],
    ['ArrowDown', 'navDown'],
    ['ArrowLeft', 'navLeft'],
    ['ArrowRight', 'navRight'],
  ] as const)('maps %s', (key, action) => {
    expect(canvasKeyAction(keyEvent(key))).toBe(action);
    // Ctrl/Cmd with a zoom key stays the zoom of the browser.
    expect(canvasKeyAction(modEvent(key))).toBeNull();
  });

  it.each([
    ['<input />', 'input'],
    ['<textarea></textarea>', 'textarea'],
    [
      '<div class="monaco-editor"><div class="view-line"></div></div>',
      '.view-line',
    ],
  ])('keeps them off in %s', (html, selector) => {
    const target = element(html, selector);
    for (const event of [
      modEvent('c', target),
      modEvent('d', target),
      keyEvent('0', target),
      keyEvent('1', target),
      keyEvent('+', target),
      keyEvent('-', target),
      keyEvent('ArrowUp', target),
      keyEvent('ArrowLeft', target),
    ]) {
      expect(canvasKeyAction(event)).toBeNull();
    }
  });

  it('leaves the native Ctrl+C to a text field of the canvas', () => {
    const copy = vi.fn();
    render(<Harness handlers={{ copy }} />);
    const input = screen.getByLabelText('search');
    const native = fireEvent.keyDown(input, { key: 'c', ctrlKey: true });
    expect(native).toBe(true);
    expect(copy).not.toHaveBeenCalled();

    const consumed = fireEvent.keyDown(screen.getByTestId('canvas'), {
      key: 'c',
      ctrlKey: true,
    });
    expect(consumed).toBe(false);
    expect(copy).toHaveBeenCalledTimes(1);
  });

  it('gives the plain text of a paste event to the paste handler', () => {
    const paste = vi.fn();
    render(<Harness handlers={{ paste }} />);
    const canvas = screen.getByTestId('canvas');
    // Ctrl+V itself is not consumed, so that the browser sends the paste event.
    expect(fireEvent.keyDown(canvas, { key: 'v', ctrlKey: true })).toBe(true);
    expect(pasteOn(canvas, '- name: a\n')).toBe(false);
    expect(paste).toHaveBeenCalledWith('- name: a\n');

    expect(pasteOn(canvas, '  ')).toBe(true);
    expect(pasteOn(screen.getByLabelText('search'), '- name: b\n')).toBe(true);
    expect(paste).toHaveBeenCalledTimes(1);
  });

  it('leaves a paste alone without a handler or when disabled', () => {
    const paste = vi.fn();
    const { rerender } = render(<Harness handlers={{}} />);
    expect(pasteOn(screen.getByTestId('canvas'), '- name: a\n')).toBe(true);
    rerender(<Harness handlers={{ paste }} enabled={false} />);
    expect(pasteOn(screen.getByTestId('canvas'), '- name: a\n')).toBe(true);
    expect(paste).not.toHaveBeenCalled();
  });
});

// parity:canvas-delete-selection
describe('CANVAS_KEY_TABLE', () => {
  it('lists every bound key with a label', () => {
    expect(CANVAS_KEY_TABLE.map((binding) => binding.action)).toEqual([
      'open',
      'rename',
      'delete',
      'palette',
      'fit',
      'clear',
      'menu',
      'help',
      'copy',
      'paste',
      'duplicate',
      'undo',
      'redo',
      'zoomIn',
      'zoomOut',
      'zoomReset',
      'navUp',
      'navDown',
      'navLeft',
      'navRight',
    ]);
    for (const binding of CANVAS_KEY_TABLE) {
      expect(binding.label).not.toBe('');
      expect(binding.keys.length).toBeGreaterThan(0);
      // Each chord of the table maps back to its own action.
      for (const chord of binding.chords) {
        expect(
          canvasKeyAction({
            ...keyEvent(chord.key),
            ctrlKey: chord.mod ?? false,
            shiftKey: chord.shift ?? false,
          })
        ).toBe(binding.action);
      }
    }
  });

  it('gives the display key of an action', () => {
    expect(canvasShortcut('delete')).toBe('Delete');
    expect(canvasShortcut('menu')).toBe('Shift+F10');
    expect(canvasShortcut('fit')).toBe('F');
    expect(canvasShortcut('copy')).toBe('Ctrl/Cmd+C');
    expect(canvasShortcut('paste')).toBe('Ctrl/Cmd+V');
    expect(canvasShortcut('duplicate')).toBe('Ctrl/Cmd+D');
    expect(canvasShortcut('undo')).toBe('Ctrl/Cmd+Z');
    expect(canvasShortcut('redo')).toBe('Ctrl/Cmd+Shift+Z');
  });

  it('maps no key event to the paste row', () => {
    const paste = CANVAS_KEY_TABLE.find(
      (binding) => binding.action === 'paste'
    );
    expect(paste?.chords).toEqual([]);
    expect(canvasKeyAction(modEvent('v'))).toBeNull();
  });

  it('maps no key event to the undo and redo rows', () => {
    for (const action of ['undo', 'redo']) {
      const binding = CANVAS_KEY_TABLE.find((b) => b.action === action);
      expect(binding?.chords).toEqual([]);
    }
    expect(canvasKeyAction(modEvent('z'))).toBeNull();
  });
});

// The keys on the real editor: each paste or duplicate is one undo step.
describe.skipIf(realSchema === null)('keys on the visual editor', () => {
  const state = { text: '' };

  function Editor() {
    const [text, setText] = useState(yamlArraySteps);
    state.text = text;
    return (
      <VisualEditor
        fileName="example.yaml"
        dagName="example"
        text={text}
        onTextChange={setText}
        validation={null}
        editable
        isDirty={text !== yamlArraySteps}
        customActions={[]}
      />
    );
  }

  function renderEditor() {
    render(
      <UserPreferencesProvider>
        <Editor />
      </UserPreferencesProvider>
    );
    return screen.getByTestId('ve-canvas');
  }

  function isSelected(key: string): boolean {
    const wrapper = screen
      .getByTestId(`ve-canvas-node-${key}`)
      .closest<HTMLElement>('[data-flow-node]')!;
    return within(wrapper)
      .getByTestId('ve-canvas-node-toolbar')
      .classList.contains('opacity-100');
  }

  it('duplicates with Ctrl+D and reverts it with one undo', () => {
    const canvas = renderEditor();
    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));
    const expected = duplicateStepsOp(parseDagDoc(yamlArraySteps), [
      'steps[1]',
    ]);
    if (!expected.ok) throw new Error(expected.message);

    fireEvent.keyDown(canvas, { key: 'd', ctrlKey: true });
    expect(state.text).toBe(expected.text);
    fireEvent.keyDown(canvas, { key: 'z', ctrlKey: true });
    expect(state.text).toBe(yamlArraySteps);
    fireEvent.keyDown(canvas, { key: 'z', ctrlKey: true, shiftKey: true });
    expect(state.text).toBe(expected.text);
  });

  it('pastes the text of a paste event and reverts it with one undo', () => {
    const canvas = renderEditor();
    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[0]'));
    pasteOn(canvas, '- name: extra\n  run: echo extra\n');
    expect(state.text).toContain(
      '  - name: extra\n    run: echo extra\n\n  - name: transform'
    );
    fireEvent.keyDown(canvas, { key: 'z', ctrlKey: true });
    expect(state.text).toBe(yamlArraySteps);
  });

  it('moves the selection along depends with the arrows', () => {
    const canvas = renderEditor();
    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[0]'));
    expect(isSelected('steps[0]')).toBe(true);
    fireEvent.keyDown(canvas, { key: 'ArrowDown' });
    expect(isSelected('steps[1]')).toBe(true);
    fireEvent.keyDown(canvas, { key: 'ArrowDown' });
    expect(isSelected('steps[2]')).toBe(true);
    fireEvent.keyDown(canvas, { key: 'ArrowUp' });
    expect(isSelected('steps[1]')).toBe(true);
    expect(state.text).toBe(yamlArraySteps);
  });
});
