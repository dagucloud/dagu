// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { ErrorModalProvider, useErrorModal } from '../error-modal';

function Trigger({
  message,
  hint,
  title,
  details,
}: {
  message: string;
  hint?: string;
  title?: string;
  details?: string[];
}) {
  const { showError } = useErrorModal();
  return (
    <button onClick={() => showError(message, hint, title, details)}>
      trigger
    </button>
  );
}

describe('ErrorModalProvider', () => {
  it('shows the message and hint', async () => {
    const user = userEvent.setup();
    render(
      <ErrorModalProvider>
        <Trigger message="Failed to save spec" hint="Check the YAML syntax." />
      </ErrorModalProvider>
    );

    await user.click(screen.getByRole('button', { name: 'trigger' }));

    expect(screen.getByText('Failed to save spec')).toBeInTheDocument();
    expect(screen.getByText('Check the YAML syntax.')).toBeInTheDocument();
  });

  it('renders detail lines in one block that preserves line breaks', async () => {
    const user = userEvent.setup();
    const details = [
      '[3:4] value is not allowed in this context',
      "field 'steps': invalid keys: nosuchfield",
    ];
    render(
      <ErrorModalProvider>
        <Trigger
          message="The spec was not saved"
          title="Validation errors"
          details={details}
        />
      </ErrorModalProvider>
    );

    await user.click(screen.getByRole('button', { name: 'trigger' }));

    expect(screen.getByText('Validation errors')).toBeInTheDocument();
    const block = screen.getByText((_, element) => {
      return (
        element?.tagName === 'PRE' &&
        element.textContent === details.join('\n')
      );
    });
    expect(block).toHaveClass('whitespace-pre-wrap');
  });
});
