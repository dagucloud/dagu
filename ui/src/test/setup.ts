// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

afterEach(() => {
  cleanup();
});

class ResizeObserverMock {
  observe(target: Element, options?: ResizeObserverOptions): void {
    void target;
    void options;
  }
  unobserve(target: Element): void {
    void target;
  }
  disconnect(): void {}
}

Object.defineProperty(globalThis, 'ResizeObserver', {
  configurable: true,
  value: ResizeObserverMock,
});

// jsdom doesn't implement scrollIntoView
Element.prototype.scrollIntoView = () => {};
