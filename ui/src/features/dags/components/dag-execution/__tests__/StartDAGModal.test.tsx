// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RuntimeProfileStatus } from '@/api/v1/schema';
import StartDAGModal from '../StartDAGModal';

const renderedFormProps = vi.fn();
const useIsAdminMock = vi.hoisted(() => vi.fn(() => true));

const previewBlock = vi.hoisted(() => ({
  reason: null as string | null,
  params: {} as Record<string, string>,
}));
const previewProps = vi.hoisted(() => vi.fn());
vi.mock('../SlotDatesPreview', () => ({
  SlotDatesPreview: (props: {
    profile?: string;
    onBlocked: (reason: string | null) => void;
    onParams: (values: Record<string, string>) => void;
  }) => {
    previewProps(props);
    const { onBlocked, onParams } = props;
    React.useEffect(() => onBlocked(previewBlock.reason), [onBlocked]);
    React.useEffect(() => onParams(previewBlock.params), [onParams]);
    return <div data-testid="slot-dates-preview" />;
  },
}));

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
      ref: React.ForwardedRef<{ validateForm: () => boolean }>
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
  // The dialog previews the dates of a DAG that declares them, and holds the
  // start while they cannot resolve.
  describe('dates preview', () => {
    const datedDag = { name: 'etl', dateNames: ['target_day'] } as never;
    const renderModal = (
      props: Partial<React.ComponentProps<typeof StartDAGModal>> = {}
    ) =>
      render(
        <StartDAGModal
          visible
          fileName="etl"
          dag={datedDag}
          dismissModal={vi.fn()}
          onSubmit={vi.fn()}
          {...props}
        />
      );

    afterEach(() => {
      previewBlock.reason = null;
      previewBlock.params = {};
      previewProps.mockClear();
    });

    // A param whose eval is one date shows that date of the slot, as a
    // placeholder: its eval still decides the value when the run starts.
    it('shows the date a param takes from the slot', async () => {
      previewBlock.params = { date: '2026-11-09' };
      renderModal({
        dag: {
          name: 'etl',
          dateNames: ['target_day'],
          paramDefs: [{ name: 'date', type: 'string' }],
          paramSchema: {
            type: 'object',
            properties: { date: { type: 'string' } },
          },
        } as never,
      });

      await waitFor(() =>
        expect(renderedFormProps).toHaveBeenLastCalledWith(
          expect.objectContaining({
            uiSchema: expect.objectContaining({
              date: expect.objectContaining({
                'ui:placeholder': '2026-11-09',
              }),
            }),
          })
        )
      );
    });

    it('previews the dates of a DAG without a schedule', () => {
      renderModal();
      expect(screen.getByTestId('slot-dates-preview')).toBeInTheDocument();
    });

    it('previews no dates for a DAG without them', () => {
      renderModal({ dag: { name: 'etl' } as never });
      expect(screen.queryByTestId('slot-dates-preview')).toBeNull();
    });

    it('holds the start while the dates cannot resolve', async () => {
      previewBlock.reason = 'dates.since needs a schedule window';
      const user = userEvent.setup();
      const onSubmit = vi.fn().mockResolvedValue(undefined);
      renderModal({ onSubmit });

      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Enqueue' })).toBeDisabled()
      );
      await user.click(
        screen.getByRole('textbox', { name: 'Run ID (optional)' })
      );
      await user.keyboard('{Enter}');
      expect(onSubmit).not.toHaveBeenCalled();
    });

    it('releases the start once the DAG has no dates', async () => {
      previewBlock.reason = 'dates.since needs a schedule window';
      const { rerender } = renderModal();
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Enqueue' })).toBeDisabled()
      );

      rerender(
        <StartDAGModal
          visible
          fileName="etl"
          dag={{ name: 'etl' } as never}
          dismissModal={vi.fn()}
          onSubmit={vi.fn()}
        />
      );
      expect(screen.getByRole('button', { name: 'Enqueue' })).toBeEnabled();
    });

    it('previews the profile the run would use', async () => {
      const user = userEvent.setup();
      renderModal({ defaultProfile: 'prod' });
      expect(previewProps).toHaveBeenLastCalledWith(
        expect.objectContaining({ profile: undefined })
      );

      await user.click(screen.getByRole('combobox', { name: /profile/i }));
      await user.click(
        await screen.findByRole('option', { name: 'No profile' })
      );
      expect(previewProps).toHaveBeenLastCalledWith(
        expect.objectContaining({ profile: '' })
      );
    });
  });

  it('groups run settings separately from DAG parameters', () => {
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
        defaultProfile="prod"
      />
    );

    const runSettings = screen.getByRole('group', { name: 'Run settings' });
    expect(
      within(runSettings).getByRole('checkbox', { name: 'Enqueue' })
    ).toBeInTheDocument();
    // The word shows once in the settings, as the checkbox label.
    expect(within(runSettings).getAllByText('Enqueue')).toHaveLength(1);
    expect(
      within(runSettings).getByRole('textbox', {
        name: 'Run ID (optional)',
      })
    ).toBeInTheDocument();
    expect(
      within(runSettings).getByRole('combobox', { name: 'Profile' })
    ).toBeInTheDocument();

    const parameters = screen.getByRole('region', { name: 'Parameters' });
    expect(within(parameters).getByLabelText(/region/i)).toBeInTheDocument();
  });

  it('leaves out parameters for a workflow without any', () => {
    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={vi.fn()}
        dag={
          {
            name: 'example-dag',
            runConfig: { disableParamEdit: true },
          } as never
        }
      />
    );

    expect(
      screen.queryByRole('region', { name: 'Parameters' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText('Parameter editing is disabled.')
    ).not.toBeInTheDocument();
  });

  // The dialog has no empty band between the run settings and the buttons.
  it('has no body band for a workflow without parameters', () => {
    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={vi.fn()}
        dag={{ name: 'example-dag' } as never}
      />
    );

    expect(screen.queryByTestId('start-dag-body')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Enqueue' })).toBeInTheDocument();
  });

  it('toggles between compact and fullscreen layouts', async () => {
    const user = userEvent.setup();

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={vi.fn()}
        dag={{ name: 'example-dag' } as never}
      />
    );

    const maximizeButton = screen.getByRole('button', {
      name: 'Maximize dialog',
    });
    expect(maximizeButton).toHaveAttribute('aria-pressed', 'false');

    await user.click(maximizeButton);

    const restoreButton = screen.getByRole('button', {
      name: 'Restore dialog',
    });
    expect(restoreButton).toHaveAttribute('aria-pressed', 'true');

    await user.click(restoreButton);

    expect(
      screen.getByRole('button', { name: 'Maximize dialog' })
    ).toHaveAttribute('aria-pressed', 'false');
  });

  it('focuses a submission error so it is visible in the scroll area', async () => {
    const user = userEvent.setup();

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={vi.fn().mockRejectedValue(new Error('Backend unavailable'))}
        dag={{ name: 'example-dag' } as never}
      />
    );

    await user.click(screen.getByRole('button', { name: 'Enqueue' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Backend unavailable');
    await waitFor(() => expect(alert).toHaveFocus());
  });

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
                count: {
                  type: 'integer',
                },
                region: {
                  type: 'string',
                  enum: ['us-east-1', 'us-west-2'],
                },
              },
            },
            paramDefs: [
              { name: 'region', type: 'string', required: false },
              { name: 'count', type: 'integer', required: false },
            ],
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
          'ui:order': ['region', 'count', '*'],
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
    fireEvent.click(screen.getByRole('button', { name: 'Enqueue' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        '{"region":"us-west-2","count":5}',
        undefined,
        false
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

  it('labels a typed field by its title and keeps its name visible', () => {
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
                title: 'AWS region',
                description: 'Region the instances are launched in.',
                type: 'string',
                required: true,
              },
            ],
          } as never
        }
      />
    );

    // The name joins the accessible name, so params sharing a title differ.
    const input = screen.getByRole('textbox', { name: 'AWS region * region' });
    // The description is read before the value is entered.
    const description = screen.getByText(
      'Region the instances are launched in.'
    );
    expect(
      description.compareDocumentPosition(input) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(input).toHaveAccessibleDescription(
      'Region the instances are launched in.'
    );
  });

  it('marks a typed field invalid and describes its error', async () => {
    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={vi.fn()}
        dag={
          {
            name: 'typed-dag',
            paramDefs: [{ name: 'region', type: 'string', required: true }],
          } as never
        }
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Enqueue' }));

    const input = screen.getByRole('textbox', { name: 'region *' });
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'));
    expect(input).toHaveAccessibleDescription('Required');
  });

  it('labels a typed boolean switch', () => {
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
                name: 'dry_run',
                title: 'Dry run',
                type: 'boolean',
                default: false,
                required: false,
              },
            ],
          } as never
        }
      />
    );

    expect(
      screen.getByRole('switch', { name: 'Dry run dry_run' })
    ).toBeInTheDocument();
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
    fireEvent.click(screen.getByRole('button', { name: 'Enqueue' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        '[{"region":"us-west-2"},{"count":"5"}]',
        undefined,
        false
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
    await user.click(screen.getByRole('button', { name: 'Enqueue' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith('', undefined, false, 'prod')
    );
  });

  // A run waits in the queue unless the user unchecks Enqueue.
  it('enqueues by default, and starts at once without Enqueue', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={onSubmit}
        dag={{ name: 'queue-dag' } as never}
      />
    );

    expect(screen.getByRole('checkbox', { name: 'Enqueue' })).toBeChecked();
    await user.click(screen.getByRole('button', { name: 'Enqueue' }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenLastCalledWith('', undefined, false)
    );

    await user.click(screen.getByRole('checkbox', { name: 'Enqueue' }));
    await user.click(screen.getByRole('button', { name: 'Start' }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenLastCalledWith('', undefined, true)
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

    await user.click(screen.getByRole('button', { name: 'Enqueue' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith('', undefined, false)
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
    await user.click(screen.getByRole('button', { name: 'Enqueue' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith('', undefined, false, '')
    );
  });

  describe('scheduled at', () => {
    const scheduledDag = {
      name: 'slot-dag',
      recentFires: [
        { time: '2026-11-11T02:00:00+09:00', timezone: 'Asia/Tokyo' },
        { time: '2026-11-10T02:00:00+09:00', timezone: 'Asia/Tokyo' },
      ],
    } as never;

    it('starts the run for a recent slot', async () => {
      const user = userEvent.setup();
      const onSubmit = vi.fn().mockResolvedValue(undefined);

      render(
        <StartDAGModal
          visible={true}
          dismissModal={vi.fn()}
          onSubmit={onSubmit}
          dag={scheduledDag}
        />
      );

      await user.click(screen.getByRole('combobox', { name: /scheduled at/i }));
      await user.click(
        await screen.findByRole('option', {
          name: '2026-11-10 02:00 (Asia/Tokyo)',
        })
      );
      await user.click(screen.getByRole('button', { name: 'Enqueue' }));

      await waitFor(() =>
        expect(onSubmit).toHaveBeenCalledWith(
          '',
          undefined,
          false,
          undefined,
          undefined,
          '2026-11-10T02:00:00+09:00'
        )
      );
    });

    // Any time other than a listed slot must be typed on purpose.
    it('requires a time when Other time is chosen', async () => {
      const user = userEvent.setup();
      const onSubmit = vi.fn().mockResolvedValue(undefined);

      render(
        <StartDAGModal
          visible={true}
          dismissModal={vi.fn()}
          onSubmit={onSubmit}
          dag={scheduledDag}
        />
      );

      await user.click(screen.getByRole('combobox', { name: /scheduled at/i }));
      await user.click(
        await screen.findByRole('option', { name: 'Other time' })
      );
      await user.click(screen.getByRole('button', { name: 'Enqueue' }));

      expect(
        await screen.findByText('Enter the scheduled time, or choose None.')
      ).toBeInTheDocument();
      expect(onSubmit).not.toHaveBeenCalled();

      await user.type(
        screen.getByRole('textbox', { name: 'Scheduled time' }),
        'latest'
      );
      await user.click(screen.getByRole('button', { name: 'Enqueue' }));
      await waitFor(() =>
        expect(onSubmit).toHaveBeenCalledWith(
          '',
          undefined,
          false,
          undefined,
          undefined,
          'latest'
        )
      );
    });

    // The slots come from the DAG's default profile, and another profile can
    // have schedules of its own.
    it('says which profile the slots follow when another is selected', async () => {
      const user = userEvent.setup();
      const hint =
        "The listed times follow the DAG's default profile. The run's window follows the selected profile.";

      render(
        <StartDAGModal
          visible={true}
          dismissModal={vi.fn()}
          onSubmit={vi.fn()}
          dag={scheduledDag}
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

      expect(screen.queryByText(hint)).not.toBeInTheDocument();
      await user.click(screen.getByRole('combobox', { name: /profile/i }));
      await user.click(await screen.findByRole('option', { name: 'prod' }));
      expect(screen.getByText(hint)).toBeInTheDocument();
    });

    it('is absent for a DAG with no recent slot', () => {
      render(
        <StartDAGModal
          visible={true}
          dismissModal={vi.fn()}
          onSubmit={vi.fn()}
          dag={{ name: 'manual-dag' } as never}
        />
      );

      expect(
        screen.queryByRole('combobox', { name: /scheduled at/i })
      ).not.toBeInTheDocument();
    });
  });

  it('can disable reuse for a build run', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <StartDAGModal
        visible={true}
        dismissModal={vi.fn()}
        onSubmit={onSubmit}
        dag={{ name: 'build-dag', type: 'build' } as never}
      />
    );

    await user.click(
      screen.getByRole('checkbox', {
        name: /disable reuse for this run/i,
      })
    );
    await user.click(screen.getByRole('button', { name: 'Enqueue' }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        '',
        undefined,
        false,
        undefined,
        true
      )
    );
  });
});
