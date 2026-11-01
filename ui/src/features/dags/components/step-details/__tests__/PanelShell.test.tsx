// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AddStepPanel } from '@/features/visual-editor/AddStepPanel';
import { WorkflowDrawer } from '@/features/visual-editor/WorkflowDrawer';
import {
  PANEL_BODY_CLASS,
  PANEL_HEADER_CLASS,
  PANEL_SHELL_CLASS,
  PANEL_ZONE_SHELL_CLASS,
  PanelHeader,
  PanelPlacementProvider,
  effectivePanelWidth,
  panelMaxWidth,
  panelWidth,
} from '../PanelShell';
import { StepSideDrawer } from '../StepSideDrawer';

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
  useQuery: vi.fn(() => ({ data: undefined })),
}));

beforeEach(() => {
  localStorage.clear();
});

const shell = () => screen.getByTestId('step-side-drawer');
const tokens = (value: string) => value.split(/\s+/).filter(Boolean);

// The owner complained that Workflow and YAML had different heights.
describe('right-side panel shell', () => {
  it('gives the Workflow and Add a step panels one geometry', () => {
    const geometry: Record<string, string[]> = {};
    const header: Record<string, string> = {};

    const workflow = render(
      <WorkflowDrawer open onClose={vi.fn()}>
        <p>Nightly</p>
      </WorkflowDrawer>
    );
    geometry.workflow = tokens(shell().className);
    header.workflow = workflow.container.ownerDocument
      .querySelector('[data-panel-header]')!
      .className.toString();
    workflow.unmount();

    const add = render(
      <StepSideDrawer
        isOpen
        onClose={vi.fn()}
        hasContent
        modal={false}
        label="Add a step"
        panelId="add-step"
      >
        <AddStepPanel
          catalog={[]}
          targetLabel="After fetch"
          recent={[]}
          onPick={vi.fn()}
          onClose={vi.fn()}
        />
      </StepSideDrawer>
    );
    geometry.add = tokens(shell().className);
    header.add = add.container.ownerDocument
      .querySelector('[data-panel-header]')!
      .className.toString();

    for (const token of tokens(PANEL_SHELL_CLASS)) {
      expect(geometry.workflow).toContain(token);
      expect(geometry.add).toContain(token);
    }
    expect(header.workflow).toBe(PANEL_HEADER_CLASS);
    expect(header.add).toBe(PANEL_HEADER_CLASS);
    // The width rule is one function: the stored width, never over the window.
    expect(panelWidth(560, 1400)).toBe('min(100vw, 560px)');
  });

  // Below 1100px a panel takes at most 45% of its zone, never under 360px.
  it('caps the width at 45% of the zone below 1100px', () => {
    expect(panelWidth(560, 1099)).toBe('min(100vw, 560px, max(45%, 360px))');
    expect(panelWidth(560, 1100)).toBe('min(100vw, 560px)');
    expect(panelMaxWidth(1024)).toBe('max(45%, 360px)');
    expect(panelMaxWidth(1100)).toBeUndefined();
    // A zone of 784px at 1024px: 45% is 353px, the floor gives 360px.
    expect(effectivePanelWidth(560, 784, 1024)).toBe(360);
    // A zone of 1000px at 1050px: 45% is 450px.
    expect(effectivePanelWidth(560, 1000, 1050)).toBe(450);
    // A stored width under the cap stays.
    expect(effectivePanelWidth(420, 1000, 1050)).toBe(420);
    // From 1100px the stored width applies, never over the window.
    expect(effectivePanelWidth(560, 860, 1100)).toBe(560);
    expect(effectivePanelWidth(960, 860, 900 + 300)).toBe(960);
  });

  it('starts under the bar of the host and ends at the bottom of the window', () => {
    const tokensOfShell = tokens(PANEL_SHELL_CLASS);
    expect(tokensOfShell).toEqual(
      expect.arrayContaining([
        'fixed',
        'bottom-0',
        'right-0',
        'top-[var(--ndv-top,0px)]',
      ])
    );
    expect(tokensOfShell).not.toContain('inset-y-0');
    // One bar height and one scrolling body for every panel.
    expect(tokens(PANEL_HEADER_CLASS)).toContain('h-12');
    expect(tokens(PANEL_BODY_CLASS)).toEqual(
      expect.arrayContaining(['min-h-0', 'flex-1', 'overflow-auto', 'p-4'])
    );
  });

  // The workspace puts the panels inside its graph zone, never over the window.
  it('keeps the panels inside a zone with one geometry', () => {
    const zone = render(
      <div data-testid="zone" className="relative">
        <PanelPlacementProvider value="zone">
          <WorkflowDrawer open onClose={vi.fn()}>
            <p>Nightly</p>
          </WorkflowDrawer>
          <StepSideDrawer
            isOpen
            onClose={vi.fn()}
            hasContent
            modal={false}
            label="Add a step"
            panelId="add-step"
          >
            <p>Body</p>
          </StepSideDrawer>
        </PanelPlacementProvider>
      </div>
    );
    const shells = screen.getAllByTestId('step-side-drawer');
    expect(shells).toHaveLength(2);
    for (const element of shells) {
      expect(zone.getByTestId('zone')).toContainElement(element);
      expect(element).not.toHaveClass('fixed');
      for (const token of tokens(PANEL_ZONE_SHELL_CLASS)) {
        expect(element).toHaveClass(token);
      }
    }
    expect(tokens(PANEL_ZONE_SHELL_CLASS)).toEqual(
      // One box from the top of the zone: its first rows, as high as the row
      // of toolbars (0 without one), are under the toolbars.
      expect.arrayContaining([
        'absolute',
        'bottom-0',
        'right-0',
        'top-0!',
        'pt-(--ww-toolbar-row,0px)',
      ])
    );
    expect(tokens(PANEL_ZONE_SHELL_CLASS)).not.toContain('fixed');
  });

  it('stays in the tree of its host, so that it reads the bar top', () => {
    render(
      <div data-testid="host">
        <StepSideDrawer isOpen onClose={vi.fn()} hasContent modal={false}>
          <p>Body</p>
        </StepSideDrawer>
      </div>
    );
    expect(screen.getByTestId('host')).toContainElement(shell());
    expect(shell()).toHaveAttribute('data-panel-shell', 'right');
  });

  it('puts the close button last in the bar', () => {
    render(
      <PanelHeader title="Workflow" closeLabel="Close" onClose={vi.fn()} />
    );
    const header = document.querySelector('[data-panel-header]')!;
    expect(header.lastElementChild).toBe(
      screen.getByRole('button', { name: 'Close' })
    );
  });
});
