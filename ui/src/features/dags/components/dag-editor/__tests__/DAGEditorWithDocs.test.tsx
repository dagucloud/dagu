// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import DAGEditorWithDocs from '../DAGEditorWithDocs';

vi.mock('../DAGEditor', () => ({
  default: () => <div data-testid="editor" />,
}));
vi.mock('../SchemaDocSidebar', () => ({
  SchemaDocSidebar: (props: { isOpen: boolean; className?: string }) =>
    props.isOpen ? (
      <aside data-testid="docs" className={props.className} />
    ) : null,
}));

// The container queries do not run in jsdom: the test pins the classes
// that put the docs under the editor of a narrow drawer.
describe('DAGEditorWithDocs layout', () => {
  afterEach(() => {
    localStorage.removeItem('schema-sidebar-open');
  });

  it('stacks the docs under the editor until the container is wide', async () => {
    localStorage.setItem('schema-sidebar-open', 'true');
    const { container } = render(<DAGEditorWithDocs value="steps: []" />);

    const docs = screen.getByTestId('docs');
    expect(docs).toHaveClass('w-full', 'h-2/5');
    expect(docs).toHaveClass('@[40rem]:w-80', '@[40rem]:border-l');
    const row = docs.parentElement as HTMLElement;
    expect(row).toHaveClass('@container', 'flex-col', '@[40rem]:flex-row');
    expect(container.contains(row)).toBe(true);
  });
});

// The code editor holds Monaco: a hidden host must not load it.
describe('DAGEditorWithDocs deferred editor', () => {
  it('mounts the editor when first shown and keeps it after', async () => {
    const { rerender } = render(
      <DAGEditorWithDocs value="steps: []" shown={false} />
    );
    expect(screen.queryByTestId('editor')).toBeNull();

    rerender(<DAGEditorWithDocs value="steps: []" shown />);
    expect(await screen.findByTestId('editor')).toBeInTheDocument();

    rerender(<DAGEditorWithDocs value="steps: []" shown={false} />);
    expect(screen.getByTestId('editor')).toBeInTheDocument();
  });

  it('mounts the editor at once without the prop', async () => {
    render(<DAGEditorWithDocs value="steps: []" />);
    expect(await screen.findByTestId('editor')).toBeInTheDocument();
  });
});
