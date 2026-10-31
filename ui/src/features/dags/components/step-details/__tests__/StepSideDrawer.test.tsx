// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RightPanelProvider,
  useActiveRightPanel,
  useRightPanel,
} from '@/contexts/RightPanelContext';
import type { components } from '@/api/v1/schema';
import { StepDetailsDrawer } from '../StepDetailsDrawer';
import { getStoredDrawerWidth, StepSideDrawer } from '../StepSideDrawer';
import { WorkflowDrawer } from '@/features/visual-editor/WorkflowDrawer';

// The step details import the API client, which needs runtime config.
vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
  useQuery: vi.fn(() => ({ data: undefined })),
}));

const WIDTH_KEY = 'dagu.stepDetailsDrawer.width';

function ActivePanel() {
  return <output>{useActiveRightPanel() ?? 'none'}</output>;
}

/** Another right-side panel that takes the place on request. */
function YamlClaimer() {
  const { claim } = useRightPanel('yaml', () => {});
  return (
    <button type="button" onClick={claim}>
      Open YAML
    </button>
  );
}

function ControlledDrawer({ onClose }: { onClose(): void }) {
  const [open, setOpen] = useState(true);
  return (
    <StepSideDrawer
      isOpen={open}
      onClose={() => {
        onClose();
        setOpen(false);
      }}
      hasContent
      modal={false}
      label="Workflow"
      panelId="workflow"
    >
      <p>Body</p>
    </StepSideDrawer>
  );
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  vi.useRealTimers();
});

// parity:drawer-mode-rules
describe('StepSideDrawer panel claim', () => {
  it('claims its panel id while it is open', () => {
    render(
      <RightPanelProvider>
        <ControlledDrawer onClose={vi.fn()} />
        <ActivePanel />
      </RightPanelProvider>
    );

    expect(screen.getByRole('status')).toHaveTextContent('workflow');
  });

  it('closes when another panel claims the place', () => {
    const onClose = vi.fn();
    render(
      <RightPanelProvider>
        <ControlledDrawer onClose={onClose} />
        <YamlClaimer />
        <ActivePanel />
      </RightPanelProvider>
    );

    act(() => {
      fireEvent.click(screen.getByRole('button', { name: 'Open YAML' }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('status')).toHaveTextContent('yaml');
  });

  it('releases its id when it closes', () => {
    const view = render(
      <RightPanelProvider>
        <StepSideDrawer isOpen onClose={vi.fn()} hasContent panelId="ndv">
          <p>Body</p>
        </StepSideDrawer>
        <ActivePanel />
      </RightPanelProvider>
    );
    expect(screen.getByRole('status')).toHaveTextContent('ndv');

    view.rerender(
      <RightPanelProvider>
        <StepSideDrawer
          isOpen={false}
          onClose={vi.fn()}
          hasContent
          panelId="ndv"
        >
          <p>Body</p>
        </StepSideDrawer>
        <ActivePanel />
      </RightPanelProvider>
    );
    expect(screen.getByRole('status')).toHaveTextContent('none');
  });

  // parity:spec-subdag-readonly-canvas
  it('passes the panel id of the step details to the drawer', () => {
    const onClose = vi.fn();
    render(
      <RightPanelProvider>
        <StepDetailsDrawer
          isOpen
          onClose={onClose}
          step={{ name: 'load' } as components['schemas']['Step']}
          panelId="subdag-step"
        />
        <YamlClaimer />
        <ActivePanel />
      </RightPanelProvider>
    );
    expect(screen.getByRole('status')).toHaveTextContent('subdag-step');

    act(() => {
      fireEvent.click(screen.getByRole('button', { name: 'Open YAML' }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('claims nothing without a panel id or a provider', () => {
    const onClose = vi.fn();
    render(
      <>
        <StepSideDrawer isOpen onClose={onClose} hasContent modal={false}>
          <p>Body</p>
        </StepSideDrawer>
        <ControlledDrawer onClose={onClose} />
        <YamlClaimer />
        <ActivePanel />
      </>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Open YAML' }));
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('status')).toHaveTextContent('none');
  });
});

// parity:workflow-panel
describe('WorkflowDrawer', () => {
  it('shows the workflow panel as the workflow right-side panel', () => {
    const onClose = vi.fn();
    render(
      <RightPanelProvider>
        <WorkflowDrawer open onClose={onClose}>
          <p>Nightly sales</p>
        </WorkflowDrawer>
        <YamlClaimer />
        <ActivePanel />
      </RightPanelProvider>
    );

    expect(
      screen.getByRole('heading', { name: 'Workflow' })
    ).toBeInTheDocument();
    expect(screen.getByText('Nightly sales')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('workflow');

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);

    act(() => {
      fireEvent.click(screen.getByRole('button', { name: 'Open YAML' }));
    });
    expect(onClose).toHaveBeenCalledTimes(2);
    expect(screen.getByRole('status')).toHaveTextContent('yaml');
  });
});

describe('WorkflowDrawer Escape', () => {
  // The focus stays on the toolbar button that opened the panel.
  it('closes on Escape while the focus is on the page', () => {
    const onClose = vi.fn();
    render(
      <>
        <button type="button">Workflow</button>
        <input aria-label="Other field" />
        <WorkflowDrawer open onClose={onClose}>
          <p>Nightly sales</p>
        </WorkflowDrawer>
      </>
    );
    screen.getByRole('button', { name: 'Workflow' }).focus();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('keeps Escape for a text field of the page', () => {
    const onClose = vi.fn();
    render(
      <>
        <input aria-label="Other field" />
        <WorkflowDrawer open onClose={onClose}>
          <p>Nightly sales</p>
        </WorkflowDrawer>
      </>
    );
    screen.getByLabelText('Other field').focus();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).not.toHaveBeenCalled();
  });

  it('does not close in a pane that the host hides', () => {
    const onClose = vi.fn();
    const { container } = render(
      <div inert>
        <WorkflowDrawer open onClose={onClose}>
          <p>Nightly sales</p>
        </WorkflowDrawer>
      </div>
    );
    expect(container).toBeTruthy();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).not.toHaveBeenCalled();
  });

  it('leaves a drawer without the option to its own focus', () => {
    const onClose = vi.fn();
    render(
      <>
        <button type="button">Elsewhere</button>
        <StepSideDrawer isOpen onClose={onClose} hasContent modal={false}>
          <p>Body</p>
        </StepSideDrawer>
      </>
    );
    screen.getByRole('button', { name: 'Elsewhere' }).focus();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).not.toHaveBeenCalled();
  });
});

describe('StepSideDrawer focus return', () => {
  function Host() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Open panel
        </button>
        <StepSideDrawer
          isOpen={open}
          onClose={() => setOpen(false)}
          hasContent={open}
          modal={false}
          label="Panel"
          closeOnPageEscape
        >
          {open && <input aria-label="Inside" autoFocus />}
        </StepSideDrawer>
      </>
    );
  }

  // A panel beside the page gives the focus back to its trigger on close,
  // not to the body: the user keeps their place in the tab order.
  it('puts the focus back on the opener after Escape from inside', () => {
    vi.useFakeTimers();
    render(<Host />);
    const opener = screen.getByRole('button', { name: 'Open panel' });
    opener.focus();
    fireEvent.click(opener);
    act(() => {
      vi.advanceTimersByTime(50);
    });
    screen.getByLabelText('Inside').focus();

    fireEvent.keyDown(window, { key: 'Escape' });
    act(() => {
      vi.advanceTimersByTime(400);
    });

    expect(opener).toHaveFocus();
  });

  it('leaves the focus alone when it is already elsewhere', () => {
    vi.useFakeTimers();
    render(
      <>
        <input aria-label="Other" />
        <Host />
      </>
    );
    const opener = screen.getByRole('button', { name: 'Open panel' });
    opener.focus();
    fireEvent.click(opener);
    act(() => {
      vi.advanceTimersByTime(50);
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open panel' }));
    screen.getByLabelText('Other').focus();
    fireEvent.keyDown(window, { key: 'Escape' });
    // The text field of the page keeps Escape: nothing closed, nothing moved.
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(screen.getByLabelText('Other')).toHaveFocus();
  });
});

// parity:side-drawer-resize
describe('StepSideDrawer box', () => {
  // A host measures the box of a panel beside the page to clear the canvas:
  // the box must not start off screen while the panel fades in.
  it('does not slide a panel beside the page', () => {
    render(<ControlledDrawer onClose={() => {}} />);
    const panel = screen.getByTestId('step-side-drawer');
    expect(panel.className).not.toMatch(/translate-x/);
    expect(panel).toHaveAttribute('data-panel-shell', 'right');
  });
});

describe('StepSideDrawer resize keys', () => {
  function renderDrawer() {
    vi.useFakeTimers();
    render(
      <StepSideDrawer isOpen onClose={vi.fn()} hasContent modal={false}>
        <p>Body</p>
      </StepSideDrawer>
    );
    act(() => {
      vi.runOnlyPendingTimers();
    });
    return screen.getByRole('separator', { name: 'Resize step details' });
  }

  it('starts at the default width', () => {
    expect(getStoredDrawerWidth()).toBe(560);
    expect(renderDrawer()).toHaveAttribute('aria-valuenow', '560');
  });

  it('widens and narrows by 40 pixels with the arrow keys', () => {
    const handle = renderDrawer();

    fireEvent.keyDown(handle, { key: 'ArrowLeft' });
    expect(handle).toHaveAttribute('aria-valuenow', '600');
    expect(localStorage.getItem(WIDTH_KEY)).toBe('600');

    fireEvent.keyDown(handle, { key: 'ArrowRight' });
    fireEvent.keyDown(handle, { key: 'ArrowRight' });
    expect(handle).toHaveAttribute('aria-valuenow', '520');
    expect(localStorage.getItem(WIDTH_KEY)).toBe('520');
  });

  it('goes to the limits with Home and End', () => {
    const handle = renderDrawer();

    fireEvent.keyDown(handle, { key: 'Home' });
    expect(handle).toHaveAttribute('aria-valuenow', '420');
    expect(localStorage.getItem(WIDTH_KEY)).toBe('420');

    fireEvent.keyDown(handle, { key: 'End' });
    expect(handle).toHaveAttribute('aria-valuenow', '960');
    expect(localStorage.getItem(WIDTH_KEY)).toBe('960');
    expect(getStoredDrawerWidth()).toBe(960);
  });
});
