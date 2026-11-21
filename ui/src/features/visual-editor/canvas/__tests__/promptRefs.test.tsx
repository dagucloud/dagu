// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Markdown } from '@/components/ui/markdown';
import { remarkReferences } from '../promptRefs';

function view(content: string) {
  return render(
    <Markdown content={content} remarkPlugins={[remarkReferences]} />
  ).container;
}

describe('the references of a Prompt card', () => {
  it('colours each reference as the editor does, in any block of the text', () => {
    const container = view(
      'Fix ${TASK} for **${params.USER}**.\n\n- item ${steps.a.outputs.x}'
    );
    const refs = [...container.querySelectorAll('span.text-info')];
    expect(refs.map((ref) => ref.textContent)).toEqual([
      '${TASK}',
      '${params.USER}',
      '${steps.a.outputs.x}',
    ]);
    expect(container.textContent).toContain('Fix ${TASK} for ${params.USER}.');
  });

  it('colours an unsupported reference as a warning', () => {
    const container = view('Run ${a-b!}.');
    expect(container.querySelector('span.text-warning')?.textContent).toBe(
      '${a-b!}'
    );
    expect(container.querySelector('span.text-info')).toBeNull();
  });

  it('leaves code and plain text alone', () => {
    const container = view('No reference here.\n\n`${TASK}`');
    expect(container.querySelector('span.text-info')).toBeNull();
    expect(container.querySelector('code')?.textContent).toBe('${TASK}');
  });
});
