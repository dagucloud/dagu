// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it } from 'vitest';
import {
  LayoutChromeProvider,
  useFullBleed,
  useHideBreadcrumb,
  useLayoutChromeState,
} from '../LayoutChromeContext';

function Host({ children }: { children?: React.ReactNode }) {
  const { fullBleed, hideBreadcrumb, value } = useLayoutChromeState();
  return (
    <LayoutChromeProvider value={value}>
      <span data-testid="full-bleed">{String(fullBleed)}</span>
      <span data-testid="hide-breadcrumb">{String(hideBreadcrumb)}</span>
      {children}
    </LayoutChromeProvider>
  );
}

function Bleed({ on = true }: { on?: boolean }) {
  useFullBleed(on);
  return null;
}

function HideCrumb({ on = true }: { on?: boolean }) {
  useHideBreadcrumb(on);
  return null;
}

function hideBreadcrumb() {
  return screen.getByTestId('hide-breadcrumb').textContent;
}

function fullBleed() {
  return screen.getByTestId('full-bleed').textContent;
}

describe('LayoutChromeContext', () => {
  it('is off when no caller asks for full bleed', () => {
    render(<Host />);

    expect(fullBleed()).toBe('false');
  });

  it('sets full bleed while the caller is mounted and clears it on unmount', () => {
    const { rerender } = render(
      <Host>
        <Bleed />
      </Host>
    );
    expect(fullBleed()).toBe('true');

    rerender(<Host />);

    expect(fullBleed()).toBe('false');
  });

  it('follows the on argument', () => {
    const { rerender } = render(
      <Host>
        <Bleed on={false} />
      </Host>
    );
    expect(fullBleed()).toBe('false');

    rerender(
      <Host>
        <Bleed on />
      </Host>
    );
    expect(fullBleed()).toBe('true');

    rerender(
      <Host>
        <Bleed on={false} />
      </Host>
    );
    expect(fullBleed()).toBe('false');
  });

  it('stays on until the last of two callers unmounts', () => {
    const { rerender } = render(
      <Host>
        <Bleed key="a" />
        <Bleed key="b" />
      </Host>
    );
    expect(fullBleed()).toBe('true');

    rerender(
      <Host>
        <Bleed key="b" />
      </Host>
    );
    expect(fullBleed()).toBe('true');

    rerender(<Host />);
    expect(fullBleed()).toBe('false');
  });

  it('does nothing without a provider', () => {
    expect(() => render(<Bleed />)).not.toThrow();
    expect(() => render(<HideCrumb />)).not.toThrow();
  });

  it('hides the breadcrumb while the caller is mounted, apart from full bleed', () => {
    const { rerender } = render(<Host />);
    expect(hideBreadcrumb()).toBe('false');

    rerender(
      <Host>
        <HideCrumb />
      </Host>
    );
    expect(hideBreadcrumb()).toBe('true');
    expect(fullBleed()).toBe('false');

    rerender(
      <Host>
        <HideCrumb on={false} />
        <Bleed />
      </Host>
    );
    expect(hideBreadcrumb()).toBe('false');
    expect(fullBleed()).toBe('true');

    rerender(<Host />);
    expect(hideBreadcrumb()).toBe('false');
  });

  it('keeps the breadcrumb hidden until the last of two callers unmounts', () => {
    const { rerender } = render(
      <Host>
        <HideCrumb key="a" />
        <HideCrumb key="b" />
      </Host>
    );
    rerender(
      <Host>
        <HideCrumb key="b" />
      </Host>
    );
    expect(hideBreadcrumb()).toBe('true');
    rerender(<Host />);
    expect(hideBreadcrumb()).toBe('false');
  });
});
