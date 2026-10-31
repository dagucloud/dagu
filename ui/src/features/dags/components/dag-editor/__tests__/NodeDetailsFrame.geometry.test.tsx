// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, within } from '@testing-library/react';
import type React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  NDV_COLUMNS_MIN_WIDTH,
  NodeDetailsFrame,
  ndvLayout,
  stepNeighbours,
  type NdvScope,
  type NdvState,
} from '../NodeDetailsFrame';
import { executionOrder } from '../stepOrder';

type Dag = components['schemas']['DAGDetails'];

const dag = {
  name: 'router',
  steps: [
    { name: 'produce', executorConfig: { type: 'http' } },
    { name: 'route', router: { value: 'x', routes: [] }, depends: ['produce'] },
    { name: 'ok', depends: ['route'] },
    { name: 'other', depends: ['route'] },
    { name: 'approve', humanTask: {} },
  ],
  handlerOn: { success: { name: 'onSuccess' } },
} as unknown as Dag;

function frame(
  props: {
    state?: NdvState;
    scope?: NdvScope;
    stepName?: string | null;
    dag?: Dag;
    onRunStep?: () => void;
    hasRunFacts?: boolean;
    problems?: string[];
    title?: string;
    formMode?: boolean;
    yamlTools?: React.ReactNode;
  } = {}
) {
  return (
    <NodeDetailsFrame
      state={props.state ?? 'expanded'}
      scope={props.scope}
      onClose={vi.fn()}
      stepName={props.stepName === undefined ? 'produce' : props.stepName}
      dag={props.dag ?? dag}
      onSelectStep={vi.fn()}
      onRunStep={props.onRunStep}
      hasRunFacts={props.hasRunFacts}
      problems={props.problems}
      yamlTools={props.yamlTools}
      title={props.title}
      formMode={props.formMode ?? false}
      canForm
      onFormModeChange={vi.fn()}
      toolbarRef={vi.fn()}
      inputRef={vi.fn()}
      outputRef={vi.fn()}
    >
      <p>center</p>
    </NodeDetailsFrame>
  );
}

const ndv = () => screen.getByTestId('ndv');

/** A frame of this width: the expanded frame takes the tabs below 1200px. */
function measured(width: number) {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    () => ({ width, height: 800 }) as DOMRect
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('node view layout by width', () => {
  // The columns need the room of the frame, not of the viewport.
  it('keeps the columns for a wide frame and the tabs for a narrow one', () => {
    expect(ndvLayout('wide', 'expanded', NDV_COLUMNS_MIN_WIDTH)).toBe(
      'columns'
    );
    expect(ndvLayout('wide', 'expanded', NDV_COLUMNS_MIN_WIDTH - 1)).toBe(
      'tabs'
    );
    expect(ndvLayout('medium', 'expanded', 528)).toBe('tabs');
    expect(ndvLayout('medium', 'expanded')).toBe('columns');
    expect(ndvLayout('narrow', 'expanded', 2000)).toBe('sheet');
  });

  it('measures the frame and switches the expanded frame to tabs', () => {
    let width = 528;
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
      () => ({ width, height: 800 }) as DOMRect
    );
    const observers: (() => void)[] = [];
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(callback: () => void) {
          observers.push(callback);
        }
        observe() {}
        disconnect() {}
      }
    );

    render(frame({ state: 'expanded' }));
    expect(ndv()).toHaveAttribute('data-layout', 'tabs');
    expect(ndv()).toHaveAttribute('data-state', 'expanded');

    width = 1200;
    act(() => observers.forEach((notify) => notify()));
    expect(ndv()).toHaveAttribute('data-layout', 'columns');
  });
});

describe('node view header', () => {
  const buttonNames = () =>
    Array.from(ndv().querySelectorAll('header button')).map((button) =>
      button.getAttribute('aria-label')
    );

  // The prev and next buttons stay where they are when the user flips
  // between the YAML and the form.
  it('keeps the same buttons in the form mode and the YAML mode', () => {
    const view = render(frame({ onRunStep: vi.fn(), formMode: false }));
    const yaml = buttonNames();
    view.rerender(frame({ onRunStep: vi.fn(), formMode: true }));
    expect(buttonNames()).toEqual(yaml);
    expect(yaml).not.toContain('Docs');
  });

  // At the docked minimum, the labels would push Close out of the panel.
  it('shows the icon only in a narrow frame, with the names kept', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
      () => ({ width: 360, height: 800 }) as DOMRect
    );
    render(frame({ onRunStep: vi.fn() }));
    for (const name of ['Run step']) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveTextContent('');
    }
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
  });

  it('keeps the labels in a wide frame', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
      () => ({ width: 700, height: 800 }) as DOMRect
    );
    render(frame({ onRunStep: vi.fn() }));
    expect(screen.getByRole('button', { name: 'Run step' })).toHaveTextContent(
      'Run step'
    );
  });
});

describe('node view buttons', () => {
  it('has no button that runs the step with its upstream steps', () => {
    render(frame({ stepName: 'ok', onRunStep: vi.fn() }));
    expect(
      screen.queryByRole('button', { name: /upstream/i })
    ).not.toBeInTheDocument();
  });

  // The node view always fills the zone, and the YAML panel stays a panel:
  // neither has a button that changes it into the other.
  it('has no button that docks, expands or enlarges the frame', () => {
    const { rerender } = render(frame({ state: 'expanded' }));
    const names = () =>
      Array.from(ndv().querySelectorAll('header button')).map((button) =>
        button.getAttribute('aria-label')
      );
    expect(names()).not.toContain('Dock to the side');
    expect(names()).not.toContain('Expand');
    rerender(frame({ state: 'docked', scope: 'file' }));
    expect(names()).not.toContain('Dock to the side');
    expect(names()).not.toContain('Expand');
    expect(names()).toEqual(['Close']);
  });
});

describe('node view tabs layout', () => {
  // The INPUT | OUTPUT strip of a narrow frame keeps room for its data.
  it('gives the shown side pane a floor of 10rem', () => {
    measured(528);
    render(frame());
    expect(ndv()).toHaveAttribute('data-layout', 'tabs');
    const input = document.getElementById('ndv-input')!;
    expect(input).toBeVisible();
    expect(input).toHaveClass('min-h-[10rem]', 'max-h-[40%]');
  });

  // On Executions the frame is narrow: the run facts must show at once.
  it('opens on OUTPUT when the host gives run facts, else on INPUT', () => {
    measured(528);
    const { unmount } = render(frame());
    expect(screen.getByRole('tab', { name: 'Input' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    unmount();
    render(frame({ hasRunFacts: true }));
    expect(screen.getByRole('tab', { name: 'Output' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });
});

describe('node view sheet layout', () => {
  // Parameters and YAML both control the center, so it is their tab panel.
  it('names the center after the selected Parameters or YAML tab', () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    const { rerender } = render(frame());
    expect(ndv()).toHaveAttribute('data-layout', 'sheet');
    const center = screen.getByTestId('ndv-center');
    expect(screen.getByRole('tabpanel', { name: 'YAML' })).toBe(center);
    rerender(frame({ formMode: true }));
    expect(screen.getByRole('tabpanel', { name: 'Parameters' })).toBe(center);
  });
});

describe('node view header', () => {
  it('has one 48px bar and a 16px title, with the choices below it', () => {
    render(frame());
    const header = ndv().querySelector('header')!;
    expect(header).toHaveClass('h-12');
    expect(screen.getByRole('heading', { name: 'produce' })).toHaveClass(
      'text-base'
    );
    // The editor choices are in the center, not in the header.
    expect(screen.getByTestId('ndv-center').firstElementChild).not.toBe(header);
    expect(header.querySelector('[data-testid="ndv-center"]')).toBeNull();
  });

  it('shows the name that the draft gives the step', () => {
    const { rerender } = render(frame());
    expect(screen.getByRole('heading', { name: 'produce' })).toBeVisible();
    rerender(frame({ title: 'produce2' }));
    expect(screen.getByRole('heading', { name: 'produce2' })).toBeVisible();
  });

  it('hides the data columns and the tabs without a step', () => {
    render(frame({ state: 'expanded', stepName: null }));
    expect(screen.getByRole('heading', { name: 'YAML' })).toBeVisible();
    expect(screen.getByLabelText('Input')).not.toBeVisible();
    expect(screen.getByLabelText('Output')).not.toBeVisible();
    expect(screen.getByRole('tablist', { hidden: true })).not.toBeVisible();
  });

  it('keeps the tabs of a step in a narrow node view', () => {
    measured(528);
    render(frame({ stepName: 'produce' }));
    expect(screen.getByRole('tablist')).toBeVisible();
  });

  // The YAML panel is the whole file: nothing of a step shows in it, even
  // with a step selected on the canvas.
  it('shows the file with no step, no tabs, no columns and no moves', () => {
    measured(528);
    render(
      frame({
        state: 'docked',
        scope: 'file',
        stepName: 'produce',
        title: 'produce',
        onRunStep: vi.fn(),
        problems: ['with.url is required'],
      })
    );
    expect(ndv()).toHaveAttribute('data-state', 'docked');
    expect(ndv()).toHaveAttribute('data-layout', 'tabs');
    expect(screen.getByRole('heading', { name: 'YAML' })).toBeVisible();
    expect(screen.getByRole('tablist', { hidden: true })).not.toBeVisible();
    expect(screen.getByLabelText('Input')).not.toBeVisible();
    expect(screen.getByLabelText('Output')).not.toBeVisible();
    for (const name of ['Previous step', 'Next step', 'Run step']) {
      expect(screen.queryByRole('button', { name })).toBeNull();
    }
    expect(screen.queryByTestId('ndv-problems')).toBeNull();
  });

  it('puts the node view of a step in the expanded state', () => {
    render(frame({ state: 'docked', scope: 'step' }));
    expect(ndv()).toHaveAttribute('data-state', 'expanded');
  });

  it('hides Run step for a lifecycle handler', () => {
    const { rerender } = render(frame({ onRunStep: vi.fn() }));
    expect(
      screen.getByRole('button', { name: 'Run step' })
    ).toBeInTheDocument();
    rerender(frame({ stepName: 'onSuccess', onRunStep: vi.fn() }));
    expect(
      screen.queryByRole('button', { name: 'Run step' })
    ).not.toBeInTheDocument();
  });

  it('shows the icon of the canvas card for a human task', () => {
    render(frame({ stepName: 'approve' }));
    expect(screen.getByTestId('ndv-step-icon')).toHaveClass('lucide-box');
  });
});

describe('node view moves', () => {
  // The steps follow the execution from the launch, not the file: `approve`
  // has no depends, so it starts with `produce`.
  it('walks the steps in execution order, then the handlers', () => {
    expect(executionOrder(dag)).toEqual([
      'produce',
      'approve',
      'route',
      'ok',
      'other',
      'onSuccess',
    ]);
    expect(stepNeighbours(dag, 'produce')).toEqual({
      previous: null,
      next: 'approve',
    });
    expect(stepNeighbours(dag, 'route')).toEqual({
      previous: 'approve',
      next: 'ok',
    });
    expect(stepNeighbours(dag, 'ok')).toEqual({
      previous: 'route',
      next: 'other',
    });
    expect(stepNeighbours(dag, 'other')).toEqual({
      previous: 'ok',
      next: 'onSuccess',
    });
    expect(stepNeighbours(dag, 'onSuccess')).toEqual({
      previous: 'other',
      next: null,
    });
  });

  // The file lists the last step first: Previous still goes to the step
  // that runs before.
  it('goes to a dependency, not to the step above in the file', () => {
    const reversed = {
      name: 'reversed',
      steps: [
        { name: 'deploy', depends: ['build'] },
        { name: 'test', depends: ['build'] },
        { name: 'build', depends: ['fetch'] },
        { name: 'fetch' },
      ],
    } as unknown as Dag;
    expect(executionOrder(reversed)).toEqual([
      'fetch',
      'build',
      'deploy',
      'test',
    ]);
    expect(stepNeighbours(reversed, 'build')).toEqual({
      previous: 'fetch',
      next: 'deploy',
    });
    expect(stepNeighbours(reversed, 'deploy').previous).toBe('build');
    expect(stepNeighbours(reversed, 'fetch').previous).toBeNull();
  });

  // The depth of a step is one more than its deepest dependency.
  it('puts a join after all its branches, and ignores unknown depends', () => {
    const diamond = {
      name: 'diamond',
      steps: [
        { name: 'join', depends: ['left', 'right', 'ghost'] },
        { name: 'right', depends: ['start'] },
        { name: 'left', depends: ['start'] },
        { name: 'start' },
      ],
    } as unknown as Dag;
    expect(executionOrder(diamond)).toEqual(['start', 'right', 'left', 'join']);
  });

  // The runtime runs a step after the steps whose outputs it reads.
  it('goes to a step that an output reference makes run before', () => {
    const inferred = {
      name: 'inferred',
      steps: [
        { name: 'report', inferredDepends: ['fetch'] },
        { name: 'fetch' },
      ],
    } as unknown as Dag;
    expect(executionOrder(inferred)).toEqual(['fetch', 'report']);
    expect(stepNeighbours(inferred, 'report').previous).toBe('fetch');
  });

  it('does not stop on a dependency cycle', () => {
    const cycle = {
      name: 'cycle',
      steps: [
        { name: 'a', depends: ['b'] },
        { name: 'b', depends: ['a'] },
        { name: 'c' },
      ],
    } as unknown as Dag;
    expect([...executionOrder(cycle)].sort()).toEqual(['a', 'b', 'c']);
  });

  it('walks steps that have no depends', () => {
    const flat = {
      name: 'agent',
      steps: [{ name: 'plan' }, { name: 'build' }, { name: 'check' }],
    } as unknown as Dag;
    expect(stepNeighbours(flat, 'build')).toEqual({
      previous: 'plan',
      next: 'check',
    });
  });
});

describe('node view with an invalid draft', () => {
  it('says why in a line at the foot of the frame', () => {
    const { rerender } = render(frame());
    expect(screen.queryByTestId('ndv-problems')).not.toBeInTheDocument();
    rerender(frame({ problems: ['with.url is required'] }));
    expect(screen.getByRole('alert')).toHaveTextContent('with.url is required');
  });

  // The validation of a draft can give a DAG without the step.
  it('keeps the icon and the moves of the last DAG that had the step', () => {
    const { rerender } = render(frame({ stepName: 'ok' }));
    expect(screen.getByRole('button', { name: 'Previous step' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Next step' })).toBeEnabled();

    const partial = { name: 'router', steps: [] } as unknown as Dag;
    rerender(frame({ stepName: 'ok', dag: partial }));

    expect(screen.getByRole('button', { name: 'Previous step' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Next step' })).toBeEnabled();
    expect(screen.getByTestId('ndv-step-icon')).not.toHaveClass(
      'lucide-file-code'
    );
  });
});

describe('NodeDetailsFrame header', () => {
  // The dock has no Unsaved chip: the Save button says the state.
  it('has no unsaved badge', () => {
    render(frame());
    expect(screen.queryByTestId('ndv-unsaved')).toBeNull();
    expect(screen.queryByText('Unsaved')).toBeNull();
  });

  // The tools of a step stand at the end of the row of the YAML | Form
  // choice; those of the file stay in the header of the docked frame.
  it('shows the text tools of a step in the row of the editor choice, not in the form', () => {
    const tools = <button type="button">Format YAML</button>;
    const { rerender } = render(frame({ yamlTools: tools }));
    const header = document.querySelector<HTMLElement>('[data-panel-header]')!;
    expect(
      within(header).queryByRole('button', { name: 'Format YAML' })
    ).toBeNull();
    expect(
      within(screen.getByTestId('ndv-center')).getByRole('button', {
        name: 'Format YAML',
      })
    ).toBeInTheDocument();
    rerender(frame({ yamlTools: tools, scope: 'file' }));
    expect(
      within(
        document.querySelector<HTMLElement>('[data-panel-header]')!
      ).getByRole('button', { name: 'Format YAML' })
    ).toBeInTheDocument();
    rerender(frame({ yamlTools: tools, formMode: true }));
    expect(screen.queryByRole('button', { name: 'Format YAML' })).toBeNull();
  });
});

describe('NodeDetailsFrame Docs button', () => {
  // The schema docs have no button in the node view: the shortcut of the
  // YAML editor stays.
  it('has no Docs button, beside the YAML or the form', () => {
    const { rerender } = render(frame());
    expect(screen.queryByRole('button', { name: /Docs/ })).toBeNull();
    rerender(frame({ formMode: true }));
    expect(screen.queryByRole('button', { name: /Docs/ })).toBeNull();
  });
});
