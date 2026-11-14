// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { RuntimeProfileEntryKind } from '@/api/v1/schema';
import { DotenvBlock } from '../DotenvBlock';
import type { EffectiveEntry } from '../runtimeProfile';

function variable(
  key: string,
  value: string,
  overrides: EffectiveEntry['overrides'] = []
): EffectiveEntry {
  return {
    key,
    kind: RuntimeProfileEntryKind.variable,
    value,
    layer: 'profile',
    overrides,
  };
}

const many = Array.from({ length: 10 }, (_, i) => variable(`K${i}`, `v${i}`));

function renderBlock(entries: EffectiveEntry[]) {
  return render(
    <DotenvBlock
      title="prod"
      testId="block"
      entries={entries}
      layerName={(layer) => (layer === 'global' ? 'Global environment' : layer)}
    />
  );
}

describe('DotenvBlock', () => {
  it('is a named group with a comment line and one line for each variable', () => {
    renderBlock([variable('REGION', 'eu'), variable('MSG', 'a b')]);
    const block = screen.getByRole('group', { name: 'prod' });
    expect(block).toHaveTextContent('# prod');
    const lines = within(block).getAllByRole('listitem');
    expect(lines.map((l) => l.textContent)).toEqual(['REGION=eu', 'MSG="a b"']);
    expect(within(block).queryByRole('button')).toBeNull();
  });

  it('colours the name, the equal sign and the value', () => {
    renderBlock([variable('PORT', '8080')]);
    const [name, equal, value] = Array.from(
      screen.getByTestId('ww-rp-row-PORT').children
    );
    expect(name).toHaveTextContent('PORT');
    expect(name!.className).toContain('text-[#9a5d0b]');
    expect(name!.className).toContain('dark:text-[#e2a566]');
    expect(equal).toHaveTextContent('=');
    expect(value).toHaveTextContent('8080');
    expect(value!.className).toContain('text-[#7c3aed]');
  });

  it('masks a secret and says so in a comment', () => {
    renderBlock([
      {
        key: 'TOKEN',
        kind: RuntimeProfileEntryKind.secret,
        value: undefined,
        layer: 'profile',
        overrides: [],
      },
    ]);
    const line = screen.getByTestId('ww-rp-row-TOKEN');
    expect(line).toHaveTextContent('TOKEN=******');
    expect(within(line).getByLabelText('Secret value hidden')).toBeVisible();
    expect(within(line).getByText('Secret')).toBeVisible();
  });

  it('ends a line with the layers that it overrides', () => {
    renderBlock([variable('TZ', 'JST', ['global', 'workspace'])]);
    expect(screen.getByTestId('ww-rp-row-TZ')).toHaveTextContent(
      'TZ=JST # overrides Global environment, workspace'
    );
  });

  it('shows the first 8 lines, then expands and collapses', () => {
    renderBlock(many);
    const block = within(screen.getByTestId('block'));
    expect(block.getAllByRole('listitem')).toHaveLength(8);
    const more = block.getByRole('button', { name: 'Show 2 more' });
    expect(more).toHaveAttribute('aria-expanded', 'false');
    expect(more.querySelector('.lucide-chevron-down')).not.toBeNull();

    fireEvent.click(more);
    expect(block.getAllByRole('listitem')).toHaveLength(10);
    const less = block.getByRole('button', { name: 'Show less' });
    expect(less).toHaveAttribute('aria-expanded', 'true');
    expect(less.querySelector('.lucide-chevron-up')).not.toBeNull();
    expect(less.getAttribute('aria-controls')).toBe(
      block.getAllByRole('list')[0]!.id
    );

    fireEvent.click(less);
    expect(block.getAllByRole('listitem')).toHaveLength(8);
  });

  it('shows no button for exactly 8 lines', () => {
    renderBlock(many.slice(0, 8));
    expect(screen.queryByRole('button')).toBeNull();
  });
});
