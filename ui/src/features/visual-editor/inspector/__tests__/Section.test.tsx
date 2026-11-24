// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
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
});
