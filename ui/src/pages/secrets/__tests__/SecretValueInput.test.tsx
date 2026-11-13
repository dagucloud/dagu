// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React, { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { SecretValueInput } from '../SecretValueInput';

const pemKey =
  '-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk=\n-----END OPENSSH PRIVATE KEY-----\n';

function Harness({
  onValue,
}: {
  onValue: (value: string) => void;
}): React.ReactElement {
  const [value, setValue] = useState('');
  return (
    <SecretValueInput
      id="secret-value"
      value={value}
      onChange={(next) => {
        setValue(next);
        onValue(next);
      }}
    />
  );
}

function renderInput(): { latest: () => string } {
  let latest = '';
  render(<Harness onValue={(value) => (latest = value)} />);
  return { latest: () => latest };
}

describe('SecretValueInput', () => {
  it('is a masked single-line input by default', () => {
    renderInput();

    expect(document.getElementById('secret-value')).toHaveAttribute(
      'type',
      'password'
    );
  });

  it('keeps line breaks in multi-line text', async () => {
    const user = userEvent.setup();
    const { latest } = renderInput();

    await user.click(screen.getByLabelText('Multi-line or file'));
    const textarea = document.getElementById('secret-value');
    expect(textarea?.tagName).toBe('TEXTAREA');
    await user.type(textarea as HTMLElement, 'line one{Enter}line two');

    expect(latest()).toBe('line one\nline two');
  });

  it('reads a file without showing its contents', async () => {
    const user = userEvent.setup();
    const { latest } = renderInput();

    await user.click(screen.getByLabelText('Multi-line or file'));
    await user.click(screen.getByRole('button', { name: 'File' }));
    const input = document.getElementById('secret-value') as HTMLInputElement;
    expect(input.type).toBe('file');
    await user.upload(input, new File([pemKey], 'id_ed25519'));

    await waitFor(() => expect(latest()).toBe(pemKey));
    expect(
      screen.getByText(`id_ed25519 loaded (${pemKey.length} bytes)`)
    ).toBeInTheDocument();
    expect(document.body.textContent).not.toContain('b3BlbnNzaC1rZXk=');
  });

  it('clears the value when the input mode changes', async () => {
    const user = userEvent.setup();
    const { latest } = renderInput();

    await user.type(
      document.getElementById('secret-value') as HTMLElement,
      'secret'
    );
    expect(latest()).toBe('secret');
    await user.click(screen.getByLabelText('Multi-line or file'));

    expect(latest()).toBe('');
  });
});
