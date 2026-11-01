// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { VisualCanvasProps } from '../../contracts';
import {
  catalogSample,
  detailsArraySteps,
  modelArraySteps,
  namesArraySteps,
} from '../../testing/fixtures';
import { buildCanvasGraph } from '../graph';
import { RenameStepPopover } from '../RenameStepPopover';
import { AGENT_NODE_KEY } from '../types';
import { VisualCanvas } from '../VisualCanvas';

const graph = buildCanvasGraph({
  model: modelArraySteps,
  names: namesArraySteps,
  details: detailsArraySteps,
  issues: { byStep: {}, dag: [] },
});

function renderCanvas(overrides: Partial<VisualCanvasProps> = {}) {
  const props: VisualCanvasProps = {
    graph,
    direction: 'LR',
    selected: 'steps[1]',
    frozen: false,
    readOnly: false,
    catalog: catalogSample,
    onSelect: vi.fn(),
    onEdit: vi.fn(),
    onCommand: vi.fn(),
    ...overrides,
  };
  render(
    <UserPreferencesProvider>
      <VisualCanvas {...props} />
    </UserPreferencesProvider>
  );
  return props;
}

function canvas(): HTMLElement {
  return screen.getByTestId('ve-canvas');
}

function nameField(): HTMLInputElement | null {
  return screen.queryByRole('textbox', { name: 'Step name' });
}

describe('RenameStepPopover', () => {
  function renderPopover() {
    const anchor = document.createElement('div');
    document.body.appendChild(anchor);
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    render(
      <RenameStepPopover
        anchor={anchor}
        name="build"
        onSubmit={onSubmit}
        onCancel={onCancel}
      />
    );
    return { onSubmit, onCancel };
  }

  it('starts with the name, focused', () => {
    renderPopover();
    expect(nameField()).toHaveValue('build');
    expect(nameField()).toHaveFocus();
  });

  it('submits a new name once on Enter', () => {
    const { onSubmit, onCancel } = renderPopover();
    fireEvent.change(nameField()!, { target: { value: ' deploy ' } });
    fireEvent.keyDown(nameField()!, { key: 'Enter' });
    fireEvent.keyDown(nameField()!, { key: 'Enter' });
    fireEvent.blur(nameField()!);
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit).toHaveBeenCalledWith('deploy');
    expect(onCancel).not.toHaveBeenCalled();
  });

  it('cancels on Esc', () => {
    const { onSubmit, onCancel } = renderPopover();
    fireEvent.change(nameField()!, { target: { value: 'deploy' } });
    fireEvent.keyDown(nameField()!, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  // Enter and Escape during an IME composition belong to the IME.
  it('ignores Enter and Escape while a composition is active', () => {
    const { onSubmit, onCancel } = renderPopover();
    fireEvent.change(nameField()!, { target: { value: 'ビルド' } });
    fireEvent.keyDown(nameField()!, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(nameField()!, { key: 'Escape', isComposing: true });
    expect(onSubmit).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
    fireEvent.keyDown(nameField()!, { key: 'Enter' });
    expect(onSubmit).toHaveBeenCalledWith('ビルド');
  });

  it('cancels on blur', () => {
    const blurred = renderPopover();
    fireEvent.blur(nameField()!);
    expect(blurred.onCancel).toHaveBeenCalledTimes(1);
    expect(blurred.onSubmit).not.toHaveBeenCalled();
  });

  it('cancels for an empty name', () => {
    const { onSubmit, onCancel } = renderPopover();
    fireEvent.change(nameField()!, { target: { value: '   ' } });
    fireEvent.keyDown(nameField()!, { key: 'Enter' });
    expect(onSubmit).not.toHaveBeenCalled();
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('cancels when the name does not change', () => {
    const { onSubmit, onCancel } = renderPopover();
    fireEvent.keyDown(nameField()!, { key: 'Enter' });
    expect(onSubmit).not.toHaveBeenCalled();
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});

describe('rename on the canvas', () => {
  it('opens with F2 and emits one renameStep on Enter', () => {
    const props = renderCanvas();
    fireEvent.keyDown(canvas(), { key: 'F2' });
    expect(nameField()).toHaveValue('transform');
    fireEvent.change(nameField()!, { target: { value: 'reshape' } });
    fireEvent.keyDown(nameField()!, { key: 'Enter' });
    expect(props.onEdit).toHaveBeenCalledTimes(1);
    expect(props.onEdit).toHaveBeenCalledWith({
      kind: 'renameStep',
      step: 'steps[1]',
      newName: 'reshape',
    });
    expect(nameField()).toBeNull();
    expect(canvas()).toHaveFocus();
  });

  // The menu has no Rename row: F2 on the selected step starts the rename.
  it('opens with F2 and cancels on Esc', () => {
    const props = renderCanvas({ selected: 'steps[0]' });
    fireEvent.keyDown(canvas(), { key: 'F2' });
    expect(nameField()).toHaveValue('fetch');
    fireEvent.change(nameField()!, { target: { value: 'pull' } });
    fireEvent.keyDown(nameField()!, { key: 'Escape' });
    expect(nameField()).toBeNull();
    expect(props.onEdit).not.toHaveBeenCalled();
  });

  it.each([
    ['read-only', { readOnly: true }],
    ['frozen', { frozen: true }],
  ] as const)('opens nothing on a %s canvas', (_, state) => {
    const props = renderCanvas(state);
    fireEvent.keyDown(canvas(), { key: 'F2' });
    expect(nameField()).toBeNull();
    expect(props.onEdit).not.toHaveBeenCalled();
  });

  it('opens nothing without a selection', () => {
    renderCanvas({ selected: null });
    fireEvent.keyDown(canvas(), { key: 'F2' });
    expect(nameField()).toBeNull();
  });

  it('does not rename the agent node', () => {
    renderCanvas({ selected: AGENT_NODE_KEY });
    fireEvent.keyDown(canvas(), { key: 'F2' });
    expect(nameField()).toBeNull();
  });
});
