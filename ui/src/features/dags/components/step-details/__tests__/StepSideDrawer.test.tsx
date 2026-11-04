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

// The Add step and launcher panels close on an Escape of the page.
describe('StepSideDrawer page Escape', () => {
  function PageDrawer({ onClose }: { onClose: () => void }) {
    return (
      <StepSideDrawer
        isOpen
        onClose={onClose}
        hasContent
        modal={false}
        closeOnPageEscape
      >
        <p>Nightly sales</p>
      </StepSideDrawer>
    );
  }

  // The focus stays on the toolbar button that opened the panel.
  it('closes on Escape while the focus is on the page', () => {
    const onClose = vi.fn();
    render(
      <>
        <button type="button">Add a step</button>
        <PageDrawer onClose={onClose} />
      </>
    );
    screen.getByRole('button', { name: 'Add a step' }).focus();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('keeps Escape for a text field of the page', () => {
    const onClose = vi.fn();
    render(
      <>
        <input aria-label="Other field" />
        <PageDrawer onClose={onClose} />
      </>
    );
    screen.getByLabelText('Other field').focus();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).not.toHaveBeenCalled();
  });

  it('does not close in a pane that the host hides', () => {
    const onClose = vi.fn();
    render(
      <div inert>
        <PageDrawer onClose={onClose} />
      </div>
    );

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).not.toHaveBeenCalled();
  });

  // The page keeps the Workflow panel as the user left it.
  it('keeps the Workflow panel open on an Escape of the page', () => {
    const onClose = vi.fn();
    render(
      <>
        <button type="button">Workflow</button>
        <WorkflowDrawer open onClose={onClose}>
          <p>Nightly sales</p>
        </WorkflowDrawer>
      </>
    );
    screen.getByRole('button', { name: 'Workflow' }).focus();

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
  // A window wide enough that the cap of a narrow window does not apply.
  beforeEach(() => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1400);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  function renderDrawer(modal = false) {
    vi.useFakeTimers();
    render(
      <StepSideDrawer isOpen onClose={vi.fn()} hasContent modal={modal}>
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

    // End leaves 240px of the 1400px window.
    fireEvent.keyDown(handle, { key: 'End' });
    expect(handle).toHaveAttribute('aria-valuemax', '1160');
    expect(handle).toHaveAttribute('aria-valuenow', '1160');
    expect(localStorage.getItem(WIDTH_KEY)).toBe('1160');
    expect(getStoredDrawerWidth()).toBe(1160);
  });

  // Below 1100px a panel beside the page shows at most 45% of its zone: the
  // handle reports and stores no more. A modal drawer has no such cap.
  it('keeps the cap of a narrow window beside the page', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1024);
    const handle = renderDrawer();
    expect(handle).toHaveAttribute('aria-valuemax', '461');
    expect(handle).toHaveAttribute('aria-valuenow', '461');

    fireEvent.keyDown(handle, { key: 'End' });
    expect(localStorage.getItem(WIDTH_KEY)).toBe('461');
    fireEvent.keyDown(handle, { key: 'Home' });
    expect(handle).toHaveAttribute('aria-valuenow', '420');
  });

  it('lets a modal drawer take all of a narrow window but a strip', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1024);
    const handle = renderDrawer(true);
    fireEvent.keyDown(handle, { key: 'End' });
    expect(handle).toHaveAttribute('aria-valuenow', '784');
    expect(localStorage.getItem(WIDTH_KEY)).toBe('784');
  });

  // A 4K window keeps a stored width far past the old cap of 960px. jsdom
  // drops a CSS min() width: the e2e layout test checks the box.
  it('keeps a wide stored width on a wide window', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(3840);
    localStorage.setItem(WIDTH_KEY, '3000');
    const handle = renderDrawer();
    expect(handle).toHaveAttribute('aria-valuemax', '3600');
    expect(handle).toHaveAttribute('aria-valuenow', '3000');
    fireEvent.keyDown(handle, { key: 'ArrowLeft' });
    expect(localStorage.getItem(WIDTH_KEY)).toBe('3040');
  });

  // A window narrower than the stored width shows less of it; a click on
  // the handle does not store that smaller width.
  it('keeps the stored width on a click without a drag', () => {
    localStorage.setItem(WIDTH_KEY, '3000');
    const handle = renderDrawer();
    expect(handle).toHaveAttribute('aria-valuenow', '1160');

    fireEvent.mouseDown(handle, { clientX: 240 });
    fireEvent.mouseUp(document);
    expect(localStorage.getItem(WIDTH_KEY)).toBe('3000');

    fireEvent.mouseDown(handle, { clientX: 240 });
    fireEvent.mouseMove(document, { clientX: 300 });
    fireEvent.mouseUp(document);
    expect(localStorage.getItem(WIDTH_KEY)).toBe('1100');
  });

  // A browser that refuses the storage keeps the new width for the page.
  it('resizes when the width cannot be stored', () => {
    const setItem = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('denied');
      });
    const handle = renderDrawer();

    fireEvent.keyDown(handle, { key: 'ArrowLeft' });
    expect(handle).toHaveAttribute('aria-valuenow', '600');
    fireEvent.keyDown(handle, { key: 'Home' });
    expect(handle).toHaveAttribute('aria-valuenow', '420');

    // Once the storage takes a width again, it is kept for the next visit
    // and the width of the page goes.
    setItem.mockRestore();
    fireEvent.keyDown(handle, { key: 'End' });
    expect(localStorage.getItem(WIDTH_KEY)).toBe('1160');
    expect(handle).toHaveAttribute('aria-valuenow', '1160');
  });
});
