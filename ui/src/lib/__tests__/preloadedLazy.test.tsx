// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { Suspense, type ComponentType } from 'react';
import { describe, expect, it } from 'vitest';
import { preloadedLazy } from '../preloadedLazy';

function Page() {
  return <p>page</p>;
}

function renderIn(Component: ComponentType) {
  render(
    <Suspense fallback={<p>loading</p>}>
      <Component />
    </Suspense>
  );
}

describe('preloadedLazy', () => {
  // A boundary that showed its fallback holds the page for up to 300 ms.
  it('renders a preloaded component in its first render', async () => {
    const page = preloadedLazy(async () => ({ default: Page }));
    await page.preload();
    renderIn(page.Component);
    expect(screen.getByText('page')).toBeInTheDocument();
    expect(screen.queryByText('loading')).toBeNull();
  });

  it('shows the fallback until a component that is not loaded arrives', async () => {
    const page = preloadedLazy(async () => ({ default: Page }));
    renderIn(page.Component);
    expect(screen.getByText('loading')).toBeInTheDocument();
    expect(await screen.findByText('page')).toBeInTheDocument();
  });
});
