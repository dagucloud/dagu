// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RuntimeProfileStatus } from '@/api/v1/schema';
import StartDAGModal from '../StartDAGModal';

const renderedFormProps = vi.fn();
const useIsAdminMock = vi.hoisted(() => vi.fn(() => true));

vi.mock('@/contexts/AuthContext', () => ({
  useIsAdmin: () => useIsAdminMock(),
}));

vi.mock('@rjsf/shadcn', async () => {
  const React = await import('react');

  return {
    default: React.forwardRef(function MockSchemaForm(
      props: {
        formData?: Record<string, unknown>;
        uiSchema?: Record<string, Record<string, unknown>>;
        onChange?: (event: { formData: Record<string, unknown> }) => void;
      },
      ref: any
    ) {
      renderedFormProps(props);
      React.useImperativeHandle(ref, () => ({
        validateForm: () => true,
      }));
      const widget = props.uiSchema?.message?.['ui:widget'];

      return (
        <div data-testid="schema-form">
          {widget === 'textarea' ? (
            <textarea aria-label="message" defaultValue="" />
          ) : (
            <input aria-label="message" defaultValue="" />
          )}
          <button
            type="button"
            onClick={() =>
              props.onChange?.({
                formData: { region: 'us-west-2', count: 5 },
              })
            }
          >
            Update schema form
          </button>
        </div>
      );
    }),
  };
});

vi.mock('@rjsf/validator-ajv8', () => ({
  default: {},
}));

Object.defineProperty(HTMLElement.prototype, 'hasPointerCapture', {
  configurable: true,
  value: () => false,
});

beforeEach(() => {
  vi.clearAllMocks();
  useIsAdminMock.mockReturnValue(true);
});

describe('StartDAGModal', () => {
  it('renders the schema-backed form path and submits a JSON object payload', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={onSubmit}
        dag={
          {
            name: 'schema-dag',
            paramSchema: {
              type: 'object',
              properties: {
                region: {
                  type: 'string',
                  enum: ['us-east-1', 'us-west-2'],
                },
                count: {
                  type: 'integer',
                },
              },
            },
            defaultParams: 'region="us-east-1" count="3"',
          } as never
        }
      />
    );

    expect(screen.getByTestId('schema-form')).toBeInTheDocument();
    expect(renderedFormProps).toHaveBeenCalledWith(
      expect.objectContaining({
        formData: { region: 'us-east-1', count: 3 },
        uiSchema: expect.objectContaining({
          region: expect.objectContaining({ 'ui:widget': 'radio' }),
        }),
        templates: expect.objectContaining({
          BaseInputTemplate: expect.any(Function),
        }),
        widgets: expect.objectContaining({
          RadioWidget: expect.any(Function),
          CheckboxWidget: expect.any(Function),
          SelectWidget: expect.any(Function),
          TextareaWidget: expect.any(Function),
        }),
      })
    );

    fireEvent.click(screen.getByRole('button', { name: 'Update schema form' }));
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        '{"region":"us-west-2","count":5}',
        undefined,
        true
      )
    );
  });

  it('does not submit a schema-backed string param when Shift+Enter is pressed', () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={onSubmit}
        dag={
          {
            name: 'schema-dag',
            paramSchema: {
              type: 'object',
              properties: {
                message: {
                  type: 'string',
                },
              },
            },
          } as never
        }
      />
    );

    const messageInput = screen.getByLabelText('message');
    expect(messageInput.tagName).toBe('TEXTAREA');
    messageInput.focus();

    fireEvent.keyDown(messageInput, {
      key: 'Enter',
      shiftKey: true,
    });

    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('falls back to typed param fields when paramSchema is absent', () => {
    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={vi.fn()}
        dag={
          {
            name: 'typed-dag',
            paramDefs: [
              {
                name: 'region',
                type: 'string',
                required: true,
              },
            ],
          } as never
        }
      />
    );

    expect(screen.queryByTestId('schema-form')).not.toBeInTheDocument();
    expect(screen.getByLabelText(/region/i)).toBeInTheDocument();
  });

  it('submits typed param fields as a JSON array payload', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={onSubmit}
        dag={
          {
            name: 'typed-dag',
            paramDefs: [
              {
                name: 'region',
                type: 'string',
                required: true,
              },
              {
                name: 'count',
                type: 'integer',
                required: true,
              },
            ],
          } as never
        }
      />
    );

    fireEvent.change(screen.getByLabelText(/region/i), {
      target: { value: 'us-west-2' },
    });
    fireEvent.change(screen.getByLabelText(/count/i), {
      target: { value: '5' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        '[{"region":"us-west-2"},{"count":"5"}]',
        undefined,
        true
      )
    );
  });

  it('marks protected profiles unavailable for non-admin users', async () => {
    useIsAdminMock.mockReturnValue(false);
    const user = userEvent.setup();

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={vi.fn()}
        dag={{ name: 'profile-dag' } as never}
        profiles={[
          {
            id: 'prod-id',
            name: 'prod',
            status: RuntimeProfileStatus.active,
            protected: true,
            description: '',
            entries: [],
            createdAt: '2026-01-01T00:00:00Z',
            updatedAt: '2026-01-01T00:00:00Z',
          },
        ]}
      />
    );

    await user.click(screen.getByRole('combobox', { name: /profile/i }));

    const protectedOption = await screen.findByRole('option', {
      name: /prod/i,
    });
    expect(protectedOption).toHaveAttribute('data-disabled');
  });

  it('submits the selected runtime profile', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={onSubmit}
        dag={{ name: 'profile-dag' } as never}
        profiles={[
          {
            id: 'prod-id',
            name: 'prod',
            status: RuntimeProfileStatus.active,
            protected: false,
            description: '',
            entries: [],
            createdAt: '2026-01-01T00:00:00Z',
            updatedAt: '2026-01-01T00:00:00Z',
          },
        ]}
      />
    );

    await user.click(screen.getByRole('combobox', { name: /profile/i }));
    await user.click(await screen.findByRole('option', { name: 'prod' }));
    await user.click(screen.getByRole('button', { name: 'Start' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith('', undefined, true, 'prod')
    );
  });

  it('omits the profile override when using the DAG default', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={onSubmit}
        dag={{ name: 'profile-dag' } as never}
        defaultProfile="prod"
      />
    );

    expect(
      screen.getByRole('combobox', { name: /profile/i })
    ).toHaveTextContent(/dag default/i);

    await user.click(screen.getByRole('button', { name: 'Start' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith('', undefined, true)
    );
  });

  it('submits an empty profile override when bypassing the DAG default', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={onSubmit}
        dag={{ name: 'profile-dag' } as never}
        defaultProfile="prod"
      />
    );

    await user.click(screen.getByRole('combobox', { name: /profile/i }));
    await user.click(await screen.findByRole('option', { name: 'No profile' }));
    await user.click(screen.getByRole('button', { name: 'Start' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith('', undefined, true, '')
    );
  });
});
