// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Section } from '../Section';

describe('Section', () => {
  it('shows its content when open and hides it when closed', () => {
    render(
      <Section title="General">
        <p>body</p>
      </Section>
    );
    const header = screen.getByRole('button', { name: 'General' });
    expect(header).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('body')).toBeInTheDocument();
    fireEvent.click(header);
    expect(header).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('body')).toBeNull();
  });

  it('starts closed on request and shows the issue count', () => {
    render(
      <Section title="YAML" defaultOpen={false} count={2}>
        <p>body</p>
      </Section>
    );
    expect(screen.queryByText('body')).toBeNull();
    expect(screen.getByRole('button', { name: /YAML/ })).toHaveTextContent('2');
    fireEvent.click(screen.getByRole('button', { name: /YAML/ }));
    expect(screen.getByText('body')).toBeInTheDocument();
  });

  it('has a focus ring on its header, not only a hover tint', () => {
    render(
      <Section title="General">
        <p>body</p>
      </Section>
    );
    expect(screen.getByRole('button', { name: 'General' }).className).toContain(
      'focus-visible:ring-2'
    );
  });

  it('opens and comes into view when its reveal key changes, not before', () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    const view = (revealKey?: number) => (
      <Section title="Tools" defaultOpen={false} revealKey={revealKey}>
        <p>body</p>
      </Section>
    );
    const { rerender } = render(view());
    expect(scroll).not.toHaveBeenCalled();
    expect(screen.queryByText('body')).toBeNull();
    rerender(view(1));
    expect(screen.getByText('body')).toBeInTheDocument();
    expect(scroll).toHaveBeenCalledTimes(1);
    // A second request opens it again after the user closed it.
    fireEvent.click(screen.getByRole('button', { name: 'Tools' }));
    expect(screen.queryByText('body')).toBeNull();
    rerender(view(2));
    expect(screen.getByText('body')).toBeInTheDocument();
    expect(scroll).toHaveBeenCalledTimes(2);
  });
});
