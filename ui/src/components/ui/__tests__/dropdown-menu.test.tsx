// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../dropdown-menu';

const press = (el: Element) =>
  fireEvent.pointerDown(el, {
    button: 0,
    ctrlKey: false,
    pointerType: 'mouse',
  });

// The outside-press listener of an open menu is added on the next tick.
const tick = () => act(() => new Promise((resolve) => setTimeout(resolve, 0)));

describe('DropdownMenuContent', () => {
  let exitAnimation: HTMLStyleElement | null = null;

  afterEach(() => {
    exitAnimation?.remove();
    exitAnimation = null;
  });

  // A closing animation keeps the content mounted for a moment. A press on
  // the trigger in that moment opens the menu again, and the closing content
  // must not close it as an outside press.
  it('opens again on a press of its trigger while it closes', async () => {
    exitAnimation = document.createElement('style');
    exitAnimation.textContent =
      '[role=menu][data-state=open]{animation-name:enter}' +
      '[role=menu][data-state=closed]{animation-name:exit}';
    document.head.append(exitAnimation);
    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Runs</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Last 5 runs</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    );
    const trigger = screen.getByRole('button', { name: 'Runs' });

    press(trigger);
    await tick();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Last 5 runs' }));
    // Still mounted: the closing animation has not ended.
    expect(trigger).toHaveAttribute('data-state', 'closed');
    expect(document.querySelector('[role=menu]')).not.toBeNull();

    press(trigger);
    await tick();
    expect(trigger).toHaveAttribute('data-state', 'open');
  });

  // React may attach a ref again while the content mounts: each attach has
  // its cleanup, and none is left once the content leaves.
  it('runs the cleanup of a callback ref when the content leaves', async () => {
    let live = 0;
    // Stable, as the ref of a caller is.
    const ref = (node: HTMLDivElement | null) => {
      if (!node) return;
      live += 1;
      return () => {
        live -= 1;
      };
    };
    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Runs</DropdownMenuTrigger>
        <DropdownMenuContent ref={ref}>
          <DropdownMenuItem>Last 5 runs</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    );

    press(screen.getByRole('button', { name: 'Runs' }));
    await tick();
    expect(live).toBe(1);
    fireEvent.click(screen.getByRole('menuitem', { name: 'Last 5 runs' }));
    expect(screen.queryByRole('menu')).toBeNull();
    expect(live).toBe(0);
  });
});
