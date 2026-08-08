// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { BUILT_IN_DOC_TEMPLATES } from '../../lib/doc-templates';
import { CreateDocModal } from '../CreateDocModal';

vi.mock('../../hooks/useDocTemplates', () => ({
  useDocTemplates: () => BUILT_IN_DOC_TEMPLATES,
  useResolveTemplateContent:
    () => async (template: { content: string }) => template.content,
}));

// Radix Select requires pointer-capture APIs missing from jsdom.
Object.defineProperty(HTMLElement.prototype, 'hasPointerCapture', {
  configurable: true,
  value: () => false,
});
Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
  configurable: true,
  value: () => {},
});

function renderModal(onSubmit = vi.fn().mockResolvedValue(undefined)) {
  render(
    <CreateDocModal
      isOpen
      onClose={() => {}}
      onSubmit={onSubmit}
      workspace={null}
    />
  );
  return onSubmit;
}

describe('CreateDocModal templates', () => {
  it('submits empty content for the default blank template', async () => {
    const user = userEvent.setup();
    const onSubmit = renderModal();

    await user.type(screen.getByLabelText('Path'), 'guides/new-doc');
    await user.click(screen.getByRole('button', { name: /create/i }));

    expect(onSubmit).toHaveBeenCalledWith('guides/new-doc', '');
  });

  it('submits the selected template content', async () => {
    const user = userEvent.setup();
    const onSubmit = renderModal();
    const runbook = BUILT_IN_DOC_TEMPLATES.find((t) => t.name === 'Runbook');

    await user.type(screen.getByLabelText('Path'), 'runbooks/etl');
    await user.click(screen.getByLabelText('Template'));
    await user.click(screen.getByRole('option', { name: 'Runbook' }));
    await user.click(screen.getByRole('button', { name: /create/i }));

    expect(onSubmit).toHaveBeenCalledWith('runbooks/etl', runbook?.content);
  });

  it('rejects an invalid path before submitting', async () => {
    const user = userEvent.setup();
    const onSubmit = renderModal();

    await user.type(screen.getByLabelText('Path'), '../escape');
    await user.click(screen.getByRole('button', { name: /create/i }));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText(/invalid|must/i)).toBeInTheDocument();
  });
});
