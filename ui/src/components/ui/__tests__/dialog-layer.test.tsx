// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ConfirmDialog } from '../confirm-dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../dropdown-menu';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../select';
import {
  Dialog,
  DialogContent,
  DialogLayerProvider,
  DialogTitle,
} from '../dialog';

function overlayOf(content: HTMLElement): HTMLElement {
  const overlay = content.previousElementSibling as HTMLElement | null;
  if (!overlay) throw new Error('no overlay');
  return overlay;
}

function Plain({ layer }: { layer?: 'default' | 'raised' }) {
  return (
    <Dialog open>
      <DialogContent layer={layer} aria-describedby={undefined}>
        <DialogTitle>Plain</DialogTitle>
      </DialogContent>
    </Dialog>
  );
}

describe('dialog layer', () => {
  it('keeps z-50 by default', () => {
    render(<Plain />);
    const content = screen.getByRole('dialog');
    expect(content).toHaveClass('z-50');
    expect(overlayOf(content)).toHaveClass('z-50');
  });

  it('lifts the content and the overlay above the node view', () => {
    render(<Plain layer="raised" />);
    const content = screen.getByRole('dialog');
    expect(content).toHaveClass('z-[80]');
    expect(content).not.toHaveClass('z-50');
    expect(overlayOf(content)).toHaveClass('z-[80]');
    expect(overlayOf(content)).not.toHaveClass('z-50');
  });

  it('lifts every dialog below a layer provider', () => {
    render(
      <DialogLayerProvider value="raised">
        <Plain />
      </DialogLayerProvider>
    );
    expect(screen.getByRole('dialog')).toHaveClass('z-[80]');
  });

  it('lets a dialog set its own layer below a provider', () => {
    render(
      <DialogLayerProvider value="raised">
        <Plain layer="default" />
      </DialogLayerProvider>
    );
    expect(screen.getByRole('dialog')).toHaveClass('z-50');
  });

  it('lifts a ConfirmDialog through its layer prop', () => {
    render(
      <ConfirmDialog
        title="Sure"
        buttonText="Go"
        visible
        dismissModal={() => undefined}
        onSubmit={() => undefined}
        layer="raised"
      >
        body
      </ConfirmDialog>
    );
    const content = screen.getByRole('dialog');
    expect(content).toHaveClass('z-[80]');
    expect(overlayOf(content)).toHaveClass('z-[80]');
  });

  it('keeps a ConfirmDialog on z-50 without the prop', () => {
    render(
      <ConfirmDialog
        title="Sure"
        buttonText="Go"
        visible
        dismissModal={() => undefined}
        onSubmit={() => undefined}
      >
        body
      </ConfirmDialog>
    );
    expect(screen.getByRole('dialog')).toHaveClass('z-50');
  });
});

describe('popup layers inside a raised dialog', () => {
  function WithSelect({ layer }: { layer?: 'default' | 'raised' }) {
    return (
      <DialogLayerProvider value="raised">
        <Dialog open>
          <DialogContent layer={layer} aria-describedby={undefined}>
            <DialogTitle>Plain</DialogTitle>
            <Select open value="a">
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="a">Option A</SelectItem>
              </SelectContent>
            </Select>
          </DialogContent>
        </Dialog>
      </DialogLayerProvider>
    );
  }

  it('lifts a Select popup above the dialog overlay', () => {
    render(<WithSelect />);
    const popup = document.querySelector('[data-slot="select-content"]');
    expect(popup).toHaveClass('z-[80]');
    expect(popup).not.toHaveClass('z-50');
  });

  it('keeps a Select popup on z-50 in a default dialog', () => {
    render(<WithSelect layer="default" />);
    const popup = document.querySelector('[data-slot="select-content"]');
    expect(popup).toHaveClass('z-50');
  });

  it('lifts a dropdown menu popup inside a raised dialog', () => {
    render(
      <DialogLayerProvider value="raised">
        <DropdownMenu open>
          <DropdownMenuTrigger>open</DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuItem>Item</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </DialogLayerProvider>
    );
    expect(screen.getByRole('menu')).toHaveClass('z-[80]');
  });
});
