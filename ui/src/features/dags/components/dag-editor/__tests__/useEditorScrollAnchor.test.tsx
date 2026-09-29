// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useEditorScrollAnchor } from '../useEditorScrollAnchor';

const VIEWPORT_HEIGHT = 800;

// jsdom has no layout engine. The stubs lay out, inside `scrollHeight` of
// scroll content, a header of `headerHeight`, the preview, and the editor at
// `previewHeight` below the preview's top. On-screen tops follow `scrollTop`
// the way a browser would, and the test reports preview resizes by hand.
let headerHeight = 0;
let previewHeight = 0;
let scrollHeight = 0;
let resizeCallbacks: ResizeObserverCallback[] = [];

function PreviewAndEditor() {
  const { anchorRef, contentRef } = useEditorScrollAnchor();
  return (
    <div data-testid="scroller" style={{ overflowY: 'auto' }}>
      <div ref={contentRef} data-testid="preview" />
      <section ref={anchorRef} data-testid="editor">
        <textarea aria-label="YAML" />
      </section>
    </div>
  );
}

function scroller(): HTMLElement {
  return screen.getByTestId('scroller');
}

function editorTop(): number {
  return screen.getByTestId('editor').getBoundingClientRect().top;
}

function reportResize() {
  act(() => {
    resizeCallbacks.forEach((callback) => callback([], {} as ResizeObserver));
  });
}

function resizePreview(by: number) {
  previewHeight += by;
  scrollHeight += by;
  reportResize();
}

function scrollTo(scrollTop: number) {
  scroller().scrollTop = scrollTop;
  fireEvent.scroll(scroller());
}

function focusEditor() {
  screen.getByLabelText('YAML').focus();
}

beforeEach(() => {
  headerHeight = 0;
  previewHeight = 100;
  scrollHeight = 5000;
  resizeCallbacks = [];
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: ResizeObserverCallback) {
        resizeCallbacks.push(callback);
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  );
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      const testId = this.dataset.testid;
      const offsets: Record<string, number> = {
        preview: headerHeight,
        editor: headerHeight + previewHeight,
      };
      const top =
        testId && testId in offsets
          ? offsets[testId]! - scroller().scrollTop
          : 0;
      const height = testId === 'scroller' ? VIEWPORT_HEIGHT : 0;
      return {
        top,
        bottom: top + height,
        left: 0,
        right: 0,
        width: 0,
        height,
        x: 0,
        y: top,
        toJSON: () => ({}),
      };
    }
  );
  const scrollerOnly = (value: () => number) =>
    function (this: HTMLElement) {
      return this.dataset.testid === 'scroller' ? value() : 0;
    };
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(
    scrollerOnly(() => VIEWPORT_HEIGHT)
  );
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(
    scrollerOnly(() => scrollHeight)
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('useEditorScrollAnchor', () => {
  it('keeps the focused editor in place while the preview resizes', () => {
    render(<PreviewAndEditor />);
    focusEditor();

    resizePreview(60);
    expect(editorTop()).toBe(100);

    // A scroll by the user moves the editor on purpose; only later preview
    // resizes are compensated.
    scrollTo(90);
    resizePreview(-40);
    expect(editorTop()).toBe(70);
  });

  // Layout above the preview moves the editor and the preview alike, so it
  // is not undone by the next preview resize.
  it('leaves movement from above the preview alone', () => {
    render(<PreviewAndEditor />);
    focusEditor();

    headerHeight += 20;
    resizePreview(60);
    expect(editorTop()).toBe(120);
  });

  // Content shrinking near the bottom makes the browser clamp the scroll
  // position before the resize is reported. That is not a user scroll.
  it('keeps the editor in place when shrinking content clamps the scroll', () => {
    scrollHeight = 2000;
    previewHeight = 1250;
    render(<PreviewAndEditor />);
    scrollTo(1150);
    focusEditor();

    previewHeight -= 100;
    scrollHeight -= 100;
    scrollTo(scrollHeight - VIEWPORT_HEIGHT);
    reportResize();
    expect(editorTop()).toBe(100);
  });

  it('lets the editor move when it does not have focus', () => {
    render(<PreviewAndEditor />);

    resizePreview(60);
    expect(editorTop()).toBe(160);
  });

  // With the editor low in the viewport the user is reading the preview, so
  // the preview stays put and the editor moves instead.
  it('lets the editor move when the preview fills most of the view', () => {
    previewHeight = VIEWPORT_HEIGHT / 2 + 100;
    render(<PreviewAndEditor />);
    focusEditor();

    resizePreview(60);
    expect(editorTop()).toBe(VIEWPORT_HEIGHT / 2 + 160);
  });
});
