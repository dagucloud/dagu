// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { StepCommandGates, VisualCanvasProps } from '../../contracts';
import {
  catalogSample,
  detailsArraySteps,
  modelArraySteps,
  namesArraySteps,
} from '../../testing/fixtures';
import { buildCanvasGraph } from '../graph';
import { KeyHelpDialog } from '../KeyHelpDialog';
import { CANVAS_KEY_TABLE } from '../useCanvasKeymap';
import { VisualCanvas } from '../VisualCanvas';

const STATUS_LINE = 'Change the status of a step from its menu';

function dialog(): HTMLElement {
  return screen.getByRole('dialog', { name: 'Keyboard shortcuts' });
}

// parity:graph-interactions-hint parity:run-graph-hint
describe('KeyHelpDialog', () => {
  it('lists every key of the key table and the pointer gestures', () => {
    render(<KeyHelpDialog open onOpenChange={vi.fn()} showSetStatus={false} />);
    const body = within(dialog());
    for (const binding of CANVAS_KEY_TABLE) {
      expect(body.getByText(binding.label)).toBeInTheDocument();
      for (const key of binding.keys) {
        expect(body.getAllByText(key).length).toBeGreaterThan(0);
      }
    }
    for (const gesture of [
      'Click',
      'Double-click',
      'Right-click',
      'Drag',
      'Ctrl/Cmd+wheel or pinch',
    ]) {
      expect(body.getByText(gesture)).toBeInTheDocument();
    }
    expect(body.queryByText(STATUS_LINE)).toBeNull();
  });

  it('shows the status line when the gates allow it', () => {
    render(<KeyHelpDialog open onOpenChange={vi.fn()} showSetStatus />);
    expect(within(dialog()).getByText(STATUS_LINE)).toBeInTheDocument();
  });
});

describe('key help on the canvas', () => {
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
      selected: null,
      frozen: false,
      readOnly: true,
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
  }

  function pressHelp() {
    fireEvent.keyDown(screen.getByTestId('ve-canvas'), {
      key: '?',
      shiftKey: true,
    });
  }

  it('opens with Shift+? and closes back to the canvas', () => {
    renderCanvas();
    expect(screen.queryByRole('dialog')).toBeNull();
    pressHelp();
    expect(dialog()).toBeInTheDocument();
    expect(within(dialog()).queryByText(STATUS_LINE)).toBeNull();
    fireEvent.keyDown(dialog(), { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('shows the status line only when a step may change status', () => {
    const gates: StepCommandGates = {
      canRunStep: false,
      canRetry: () => false,
      canSetStatus: () => true,
      hasSubRuns: () => false,
    };
    renderCanvas({ stepCommandGates: gates });
    pressHelp();
    expect(within(dialog()).getByText(STATUS_LINE)).toBeInTheDocument();
  });

  it('hides the status line when no step may change status', () => {
    renderCanvas({
      stepCommandGates: {
        canRunStep: true,
        canRetry: () => true,
        canSetStatus: () => false,
        hasSubRuns: () => true,
      },
    });
    pressHelp();
    expect(within(dialog()).queryByText(STATUS_LINE)).toBeNull();
  });
});
