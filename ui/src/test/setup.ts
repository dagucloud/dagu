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

// jsdom doesn't implement the Pointer Capture API, which Radix's Select uses
// to track a press that starts on the trigger.
Element.prototype.hasPointerCapture = () => false;
Element.prototype.setPointerCapture = () => {};
Element.prototype.releasePointerCapture = () => {};

// jsdom has no canvas: its getContext logs "not implemented" and returns null.
// A test that draws installs its own context with `vi.spyOn`.
HTMLCanvasElement.prototype.getContext = () => null;

// The app page defines getConfig(). Modules such as hooks/api read it when they
// are imported, so a test that imports one without mocking it needs a default.
if (typeof (globalThis as { getConfig?: unknown }).getConfig === 'undefined') {
  Object.defineProperty(globalThis, 'getConfig', {
    configurable: true,
    writable: true,
    value: () => ({ apiURL: '/api/v1', basePath: '', version: 'test' }),
  });
}
