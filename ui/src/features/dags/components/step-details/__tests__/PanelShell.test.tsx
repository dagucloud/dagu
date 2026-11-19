// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AddStepPanel } from '@/features/visual-editor/AddStepPanel';
import { CanvasToolbarHostContext } from '@/features/visual-editor/canvas/toolbarHost';
import { WorkflowDrawer } from '@/features/visual-editor/WorkflowDrawer';
import {
  PANEL_BODY_CLASS,
  PANEL_HEADER_CLASS,
  PANEL_SHELL_CLASS,
  PANEL_ZONE_SHELL_CLASS,
  PanelHeader,
  PanelPlacementProvider,
  clampPanelWidth,
  effectivePanelWidth,
  panelMaxWidth,
  panelMaxWidthIn,
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
    expect(panelWidth(560, 1400)).toBe(
      'min(100vw, 560px, max(100% - 360px, 360px))'
    );
  });

  // The toolbar row of a zone holds the Workflow toggle, which names the panel
  // from md. The close button stays below md, where the row is hidden.
  it('drops the title bar of the Workflow panel from md in a zone with a row', () => {
    const zone = (row: HTMLElement | undefined) => (
      <PanelPlacementProvider value="zone">
        <CanvasToolbarHostContext.Provider value={row}>
          <WorkflowDrawer open onClose={vi.fn()}>
            <p>Nightly</p>
          </WorkflowDrawer>
        </CanvasToolbarHostContext.Provider>
      </PanelPlacementProvider>
    );
    const row = render(zone(document.createElement('div')));
    expect(shell().querySelector('[data-panel-header]')).toHaveClass(
      'md:hidden'
    );
    // Two Close buttons: the one of the hidden header (below md) and the one
    // of the band of the row, at the right end of the panel.
    expect(screen.getAllByRole('button', { name: 'Close' })).toHaveLength(2);
    // The panel names itself in the band of the row, over its first rows.
    expect(screen.getByTestId('panel-row-title')).toHaveTextContent('Workflow');
    expect(
      shell().querySelector('[data-panel-close="row"]')?.querySelector('button')
    ).toHaveAccessibleName('Close');
    row.unmount();

    render(zone(undefined));
    expect(shell().querySelector('[data-panel-header]')).not.toHaveClass(
      'md:hidden'
    );
    expect(screen.queryByTestId('panel-row-title')).toBeNull();
  });

  // Below 1100px a panel takes at most 45% of its zone, never under 360px.
  it('caps the width at 45% of the zone below 1100px', () => {
    expect(panelWidth(560, 1099)).toBe('min(100vw, 560px, max(45%, 360px))');
    expect(panelMaxWidth(1024)).toBe('max(45%, 360px)');
    // A zone of 784px at 1024px: 45% is 353px, the floor gives 360px.
    expect(effectivePanelWidth(560, 784, 1024)).toBe(360);
    // A zone of 1000px at 1050px: 45% is 450px.
    expect(effectivePanelWidth(560, 1000, 1050)).toBe(450);
    // A stored width under the cap stays.
    expect(effectivePanelWidth(420, 1000, 1050)).toBe(420);
    // From 1100px the stored width applies, never over the window.
    expect(effectivePanelWidth(560, 1000, 1100)).toBe(560);
    expect(effectivePanelWidth(960, 1458, 1700)).toBe(960);
  });

  // From 1100px the graph beside a panel keeps 360px at least.
  it('leaves 360px to the graph from 1100px', () => {
    expect(panelWidth(900, 1100)).toBe(
      'min(100vw, 900px, max(100% - 360px, 360px))'
    );
    expect(panelMaxWidth(1100)).toBe('max(100% - 360px, 360px)');
    // A zone of 1038px at 1280px: the panel takes 678px at most.
    expect(effectivePanelWidth(960, 1038, 1280)).toBe(678);
    // A stored width under the cap stays.
    expect(effectivePanelWidth(560, 1038, 1280)).toBe(560);
    // A narrow zone keeps the floor of the panel.
    expect(effectivePanelWidth(560, 600, 1280)).toBe(360);
    // The wide limit of the panel is the zone less the room of the graph.
    expect(panelMaxWidthIn(1100)).toBe(740);
    expect(panelMaxWidthIn(3600)).toBe(3240);
    expect(panelMaxWidthIn(500)).toBe(360);
    expect(clampPanelWidth(100, 1600)).toBe(420);
    expect(clampPanelWidth(5000, 1600)).toBe(1240);
    expect(clampPanelWidth(900.4, 1600)).toBe(900);
    // A 4K window with the sidebar: a zone of 3600px.
    expect(effectivePanelWidth(5000, 3600, 3840)).toBe(3240);
    expect(effectivePanelWidth(960, 860, 1200)).toBe(500);
  });

  // The resize handle of a panel beside the page stops where panelWidth
  // caps the panel; a modal panel gives no viewport and has no such cap.
  it('bounds the resize handle as panelWidth caps the panel', () => {
    expect(panelMaxWidthIn(1600, 1600)).toBe(1240);
    expect(panelMaxWidthIn(1000, 1050)).toBe(450);
    expect(panelMaxWidthIn(784, 1024)).toBe(360);
    expect(panelMaxWidthIn(784)).toBe(424);
    expect(clampPanelWidth(900, 1000, 1050)).toBe(450);
    // A cap under the narrowest width is the one width left.
    expect(clampPanelWidth(100, 784, 1024)).toBe(360);
    expect(clampPanelWidth(900, 784, 1024)).toBe(360);
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
