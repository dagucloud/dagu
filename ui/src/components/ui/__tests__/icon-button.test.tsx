// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Play } from 'lucide-react';
import { describe, expect, it, vi } from 'vitest';
import { IconButton } from '@/components/ui/icon-button';
import { Spinner } from '@/components/ui/spinner';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';

function sizeClasses(el: HTMLElement): string[] {
  return el.className.split(/\s+/).filter((c) => /^size-|^h-|^w-/.test(c));
}

describe('IconButton', () => {
  it('keeps the same size classes when idle, busy and disabled', () => {
    render(
      <div>
        <IconButton label="Idle" icon={Play} size="sm" />
        <IconButton label="Busy" icon={Play} size="sm" busy />
        <IconButton label="Off" icon={Play} size="sm" disabledReason="No" />
      </div>
    );
    const idle = sizeClasses(screen.getByRole('button', { name: 'Idle' }));
    expect(idle).toContain('size-6');
    expect(sizeClasses(screen.getByRole('button', { name: 'Busy' }))).toEqual(
      idle
    );
    expect(sizeClasses(screen.getByRole('button', { name: 'Off' }))).toEqual(
      idle
    );
  });

  it('uses 28 px for md and puts a spinner in the icon slot', () => {
    render(<IconButton label="Run" icon={Play} busy />);
    const button = screen.getByRole('button', { name: 'Run' });
    expect(button).toHaveClass('size-7');
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(button.querySelectorAll('svg')).toHaveLength(1);
    expect(button.querySelector('svg')).toHaveClass('animate-spin');
  });

  it('ignores clicks while busy', async () => {
    const onClick = vi.fn();
    render(<IconButton label="Run" icon={Play} busy onClick={onClick} />);
    await userEvent.click(screen.getByRole('button', { name: 'Run' }));
    expect(onClick).not.toHaveBeenCalled();
  });

  it('sets aria-pressed and a pressed background', () => {
    render(
      <div>
        <IconButton label="On" icon={Play} pressed />
        <IconButton label="Off" icon={Play} pressed={false} />
        <IconButton label="Plain" icon={Play} />
      </div>
    );
    const on = screen.getByRole('button', { name: 'On' });
    expect(on).toHaveAttribute('aria-pressed', 'true');
    expect(on).toHaveClass('bg-muted');
    expect(screen.getByRole('button', { name: 'Off' })).toHaveAttribute(
      'aria-pressed',
      'false'
    );
    expect(screen.getByRole('button', { name: 'Plain' })).not.toHaveAttribute(
      'aria-pressed'
    );
  });

  it('shows the label, shortcut and description in the tooltip', async () => {
    render(
      <IconButton
        label="Run step"
        icon={Play}
        shortcut="Ctrl+R"
        description="Runs this step alone"
      />
    );
    await userEvent.hover(screen.getByRole('button', { name: 'Run step' }));
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('Run step');
    expect(tip).toHaveTextContent('Ctrl+R');
    expect(tip).toHaveTextContent('Runs this step alone');
  });

  it('shows the disabled reason in the tooltip', async () => {
    const onClick = vi.fn();
    render(
      <IconButton
        label="Run step"
        icon={Play}
        disabledReason="Save the workflow first"
        onClick={onClick}
      />
    );
    const button = screen.getByRole('button', { name: 'Run step' });
    expect(button).toBeDisabled();
    // The wrapper takes the pointer event that the disabled button cannot.
    await userEvent.hover(button.parentElement as HTMLElement);
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('Save the workflow first');
    expect(onClick).not.toHaveBeenCalled();
  });
});

// A second Radix tooltip that opens closes the first one and its content:
// the button of a tooltip keeps working, and the title says its name.
describe('IconButton in a tooltip', () => {
  it('stays clickable and names itself with a title', async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(
      <Tooltip>
        <TooltipTrigger asChild>
          <span tabIndex={0} data-testid="host">
            host
          </span>
        </TooltipTrigger>
        <TooltipContent>
          <IconButton label="Copy it" icon={Play} onClick={onClick} />
        </TooltipContent>
      </Tooltip>
    );
    await user.hover(screen.getByTestId('host'));
    const tip = await screen.findByRole('tooltip');
    const button = within(tip).getByRole('button', { name: 'Copy it' });
    expect(button).toHaveAttribute('title', 'Copy it');

    await user.click(button);

    expect(onClick).toHaveBeenCalledTimes(1);
    expect(screen.getAllByRole('tooltip')).toHaveLength(1);
  });
});

describe('Spinner', () => {
  it('is hidden from assistive technology without a label', () => {
    const { container } = render(<Spinner />);
    const svg = container.querySelector('svg');
    expect(svg).toHaveAttribute('aria-hidden', 'true');
    expect(svg).toHaveClass('size-3.5', 'animate-spin');
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('is a named status with a label', () => {
    render(<Spinner label="Loading" />);
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument();
  });
});
