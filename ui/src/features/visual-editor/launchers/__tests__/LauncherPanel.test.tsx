// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { useState, type ReactElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  ParamDefType,
  ScheduleKind,
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { readSchedule, type ScheduleView } from '../../doc';
import { CronLauncherPanel } from '../LauncherPanel';

// UTC+14 without daylight saving: unlikely to be the zone of the test host.
const SCHEDULER_TZ = 'Etc/GMT-14';

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: SCHEDULER_TZ }),
}));

/** A schedule of plain cron entries, as readSchedule reads it. */
function cronSchedule(
  expressions: string[],
  params: Array<Record<string, string>> = []
): ScheduleView {
  return {
    present: true,
    expressions,
    others: 0,
    entries: expressions.map((expression, index) => ({
      expressionIndex: index,
      schedule: { kind: ScheduleKind.cron, expression },
      params: params[index] ?? {},
    })),
  };
}

function renderPanel(
  expressions: string[],
  onChange: (expressions: string[]) => void = () => undefined
) {
  render(
    <CronLauncherPanel
      schedule={cronSchedule(expressions)}
      editable
      onChange={onChange}
      onClose={() => undefined}
    />
  );
}

/** A panel that keeps the schedule it writes, as the editor does. */
function StatefulPanel({ initial }: { initial: string[] }): ReactElement {
  const [expressions, setExpressions] = useState(initial);
  return (
    <CronLauncherPanel
      schedule={cronSchedule(expressions)}
      editable
      onChange={setExpressions}
      onClose={() => undefined}
    />
  );
}

describe('CronLauncherPanel', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('refuses an edit to an expression of another row', () => {
    const onChange = vi.fn();
    renderPanel(['0 * * * *', '@daily'], onChange);
    const second = screen.getAllByLabelText('Cron expression')[1]!;
    fireEvent.change(second, { target: { value: '0 * * * *' } });
    fireEvent.blur(second);
    expect(onChange).not.toHaveBeenCalled();
    expect(second).toHaveValue('@daily');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'This expression is already in the schedule.'
    );
  });

  it('refuses to add an expression that is already listed', () => {
    renderPanel(['@daily']);
    fireEvent.change(screen.getByLabelText('New cron expression'), {
      target: { value: '@daily' },
    });
    expect(screen.getByRole('button', { name: 'Add schedule' })).toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent(
      'This expression is already in the schedule.'
    );
  });

  // Enter of an input method confirms its text, not the expression.
  it('ignores Enter and Escape while an input method composes', () => {
    const onChange = vi.fn();
    renderPanel(['@daily'], onChange);
    const row = screen.getByLabelText('Cron expression');
    fireEvent.change(row, { target: { value: '@hourly' } });
    fireEvent.keyDown(row, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(row, { key: 'Escape', isComposing: true });
    expect(onChange).not.toHaveBeenCalled();
    expect(row).toHaveValue('@hourly');
    fireEvent.keyDown(row, { key: 'Enter' });
    expect(onChange).toHaveBeenLastCalledWith(['@hourly']);

    const added = screen.getByLabelText('New cron expression');
    fireEvent.change(added, { target: { value: '@weekly' } });
    fireEvent.keyDown(added, { key: 'Enter', isComposing: true });
    expect(onChange).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(added, { key: 'Enter' });
    expect(onChange).toHaveBeenLastCalledWith(['@daily', '@weekly']);
  });

  // The server knows what the browser cannot, such as the scheduler's own
  // rules, so its runs replace the preview of a saved expression. A draft
  // still previews at once.
  it('lists the server runs and previews only the expression being typed', () => {
    render(
      <CronLauncherPanel
        schedule={cronSchedule(['0 9 * * *'])}
        editable
        upcomingRuns={[
          { time: '2027-01-04T09:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
        ]}
        schedules={[{ expression: '0 9 * * *', kind: ScheduleKind.cron }]}
        onChange={() => undefined}
        onClose={() => undefined}
      />
    );

    expect(screen.getByText('Mon 2027-01-04 09:00')).toBeInTheDocument();
    expect(screen.queryByTestId('ve-launcher-next-runs')).toBeNull();

    fireEvent.change(screen.getByLabelText('Cron expression'), {
      target: { value: '0 10 * * *' },
    });
    expect(screen.getByTestId('ve-launcher-next-runs')).toBeInTheDocument();
  });

  // The scheduler fires in its own time zone, whatever the zone of the
  // browser.
  it('previews the next runs in the time zone of the scheduler', () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-10-30T00:00:00Z'));
    renderPanel(['0 9 * * *']);
    const runs = [
      '2026-10-30T19:00:00Z',
      '2026-10-31T19:00:00Z',
      '2026-11-01T19:00:00Z',
    ].map((iso) => formatAbsoluteTime(iso, { seconds: false }));
    expect(screen.getByTestId('ve-launcher-next-runs')).toHaveTextContent(
      `Next runs: ${runs.join(', ')}`
    );
  });

  // The form is the way to a schedule without cron syntax: its controls
  // write the expression that the Add button saves.
  it('builds each kind of schedule in the form', () => {
    const onChange = vi.fn();
    renderPanel([], onChange);
    const frequency = () => screen.getByLabelText('Frequency');
    const added = screen.getByLabelText('New cron expression');

    fireEvent.change(frequency(), { target: { value: 'minutes' } });
    fireEvent.change(screen.getByLabelText('Minutes between runs'), {
      target: { value: '10' },
    });
    expect(added).toHaveValue('*/10 * * * *');

    fireEvent.change(frequency(), { target: { value: 'hourly' } });
    fireEvent.change(screen.getByLabelText('Minute of the hour'), {
      target: { value: '30' },
    });
    expect(added).toHaveValue('30 * * * *');

    fireEvent.change(frequency(), { target: { value: 'daily' } });
    fireEvent.change(screen.getByLabelText('Time of day'), {
      target: { value: '18:30' },
    });
    expect(added).toHaveValue('30 18 * * *');

    fireEvent.change(frequency(), { target: { value: 'weekly' } });
    fireEvent.click(screen.getByRole('button', { name: 'Wednesday' }));
    fireEvent.click(screen.getByRole('button', { name: 'Friday' }));
    expect(added).toHaveValue('30 18 * * 1,3,5');
    expect(screen.getByTestId('ve-schedule-summary')).toHaveTextContent(
      'Every week on Mon, Wed, Fri at 18:30'
    );

    fireEvent.change(frequency(), { target: { value: 'monthly' } });
    fireEvent.change(screen.getByLabelText('Day of the month'), {
      target: { value: '31' },
    });
    expect(added).toHaveValue('30 18 31 * *');
    expect(
      screen.getByText('A month without day 31 has no run.')
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        `Times are in the time zone of the scheduler (${SCHEDULER_TZ}).`
      )
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Add schedule' }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith(['30 18 31 * *']);
  });

  it('keeps the last day of a weekly schedule', () => {
    renderPanel([]);
    fireEvent.change(screen.getByLabelText('Frequency'), {
      target: { value: 'weekly' },
    });
    expect(screen.getByRole('button', { name: 'Monday' })).toBeDisabled();
  });

  it('shows in the form an expression typed in the field', () => {
    renderPanel([]);
    fireEvent.change(screen.getByLabelText('New cron expression'), {
      target: { value: '0 9 * * MON-FRI' },
    });
    expect(screen.getByLabelText('Frequency')).toHaveValue('weekly');
    expect(screen.getByLabelText('Time of day')).toHaveValue('09:00');
    expect(screen.getByRole('button', { name: 'Friday' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(screen.getByRole('button', { name: 'Saturday' })).toHaveAttribute(
      'aria-pressed',
      'false'
    );
  });

  // Opening the form of a row reads the expression and writes nothing. An
  // expression outside the form stays until a frequency replaces it.
  it('leaves an expression unchanged until the form is edited', () => {
    const onChange = vi.fn();
    renderPanel(['CRON_TZ=Asia/Tokyo 0 9 * * *', '0 9 * * MON-FRI'], onChange);
    const rows = within(screen.getByLabelText('Schedules')).getAllByRole(
      'listitem'
    );
    const custom = within(rows[0]!);
    const weekdays = within(rows[1]!);

    fireEvent.click(custom.getByRole('button', { name: 'Edit with the form' }));
    expect(custom.getByLabelText('Frequency')).toHaveDisplayValue(
      'Custom expression'
    );
    expect(custom.queryByTestId('ve-schedule-summary')).toBeNull();

    fireEvent.click(
      weekdays.getByRole('button', { name: 'Edit with the form' })
    );
    expect(weekdays.getByLabelText('Frequency')).toHaveValue('weekly');
    expect(weekdays.getByLabelText('Cron expression')).toHaveValue(
      '0 9 * * MON-FRI'
    );
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.click(weekdays.getByRole('button', { name: 'Saturday' }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith([
      'CRON_TZ=Asia/Tokyo 0 9 * * *',
      '0 9 * * 1-6',
    ]);
  });

  // An edit in the form of a row is saved at once, and the form stays open
  // for the next one.
  it('keeps the form of a row open across its edits', () => {
    render(<StatefulPanel initial={['0 9 * * *']} />);
    const row = within(screen.getByLabelText('Schedules'));
    fireEvent.click(row.getByRole('button', { name: 'Edit with the form' }));
    fireEvent.change(row.getByLabelText('Time of day'), {
      target: { value: '10:15' },
    });
    expect(row.getByLabelText('Cron expression')).toHaveValue('15 10 * * *');
    fireEvent.change(row.getByLabelText('Frequency'), {
      target: { value: 'monthly' },
    });
    expect(row.getByLabelText('Cron expression')).toHaveValue('15 10 1 * *');
    expect(row.getByTestId('ve-schedule-summary')).toHaveTextContent(
      'Every month on day 1 at 10:15'
    );
  });

  // The open form belongs to its expression, not to its place in the list.
  it('does not open the form of the row that follows a removed row', () => {
    render(<StatefulPanel initial={['0 9 * * *', '0 18 * * *']} />);
    const list = within(screen.getByLabelText('Schedules'));
    fireEvent.click(
      list.getAllByRole('button', { name: 'Edit with the form' })[0]!
    );
    expect(list.getByTestId('ve-schedule-builder')).toBeInTheDocument();

    fireEvent.click(
      list.getAllByRole('button', { name: 'Remove schedule' })[0]!
    );
    expect(list.getByLabelText('Cron expression')).toHaveValue('0 18 * * *');
    expect(list.queryByTestId('ve-schedule-builder')).toBeNull();
  });
});

describe('CronLauncherPanel parameter overrides', () => {
  const defaults = [
    {
      name: 'mode',
      type: ParamDefType.string,
      required: false,
      default: 'manual',
    },
  ];

  function renderParams(
    params: Array<Record<string, string>>,
    expressions = ['0 5 * * *', '0 6 * * *']
  ) {
    const onParamChange = vi.fn();
    const onChange = vi.fn();
    render(
      <CronLauncherPanel
        schedule={cronSchedule(expressions, params)}
        editable
        paramDefs={defaults}
        onChange={onChange}
        onParamChange={onParamChange}
        onClose={() => undefined}
      />
    );
    return { onParamChange, onChange };
  }

  it('shows the workflow default as a placeholder on every schedule', () => {
    renderParams([{}, {}]);
    const fields = screen.getAllByLabelText('mode');
    expect(fields).toHaveLength(2);
    fields.forEach((field) => {
      expect(field).toHaveValue('');
      expect(field).toHaveAttribute('placeholder', 'manual');
    });
    expect(
      screen.queryByRole('button', { name: /Use workflow default/ })
    ).toBeNull();
  });

  // Each sent value is one YAML edit and one undo step, so typing alone
  // sends nothing.
  it('sets an override of the edited schedule on blur', () => {
    const { onParamChange } = renderParams([{}, {}]);
    const field = screen.getAllByLabelText('mode')[1]!;
    fireEvent.change(field, { target: { value: 'z' } });
    expect(onParamChange).not.toHaveBeenCalled();
    fireEvent.blur(field);
    expect(onParamChange).toHaveBeenCalledOnce();
    expect(onParamChange).toHaveBeenCalledWith(1, 'mode', 'z');
  });

  it('sets an override on Enter after the input method finishes', () => {
    const { onParamChange } = renderParams([{}, {}]);
    const field = screen.getAllByLabelText('mode')[0]!;
    fireEvent.change(field, { target: { value: '本番' } });
    fireEvent.keyDown(field, { key: 'Enter', isComposing: true });
    expect(onParamChange).not.toHaveBeenCalled();
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(onParamChange).toHaveBeenCalledWith(0, 'mode', '本番');
  });

  it('keeps an explicit value that equals the default', () => {
    const { onParamChange } = renderParams([{ mode: 'x' }, {}]);
    const field = screen.getAllByLabelText('mode')[0]!;
    fireEvent.change(field, { target: { value: 'manual' } });
    fireEvent.blur(field);
    expect(onParamChange).toHaveBeenCalledWith(0, 'mode', 'manual');
  });

  it('resets an override with the button beside its field', () => {
    const { onParamChange } = renderParams([{}, { mode: 'z' }]);
    expect(screen.getAllByLabelText('mode')[1]).toHaveValue('z');
    const reset = screen.getByRole('button', {
      name: 'Use workflow default for mode',
    });
    fireEvent.click(reset);
    expect(onParamChange).toHaveBeenCalledWith(1, 'mode', null);
  });

  it('clears an override when its field is emptied', () => {
    const { onParamChange } = renderParams([{ mode: 'z' }, {}]);
    const field = screen.getAllByLabelText('mode')[0]!;
    fireEvent.change(field, { target: { value: '' } });
    fireEvent.blur(field);
    expect(onParamChange).toHaveBeenCalledWith(0, 'mode', null);
  });

  // The default of a computed param is only the fallback of a failed eval.
  it('shows a computed param as computed, not its fallback', () => {
    render(
      <CronLauncherPanel
        schedule={cronSchedule(['0 5 * * *'])}
        editable
        paramDefs={[
          {
            name: 'date',
            type: ParamDefType.string,
            required: false,
            default: '1970-01-01',
            computed: true,
          },
        ]}
        onChange={() => undefined}
        onClose={() => undefined}
      />
    );
    expect(screen.getByLabelText('date')).toHaveAttribute(
      'placeholder',
      'Computed when the run starts'
    );
  });

  // The override applies to the run although the workflow no longer
  // declares the name, so it must stay visible and removable.
  it('shows and resets an override of an undeclared name', () => {
    const { onParamChange } = renderParams([{ mode: 'y', region: 'eu' }]);
    expect(screen.getByLabelText('region')).toHaveValue('eu');
    expect(
      screen.getByText('Not a parameter of this workflow')
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: 'Use workflow default for region' })
    );
    expect(onParamChange).toHaveBeenCalledWith(0, 'region', null);
  });

  it('marks an override set to the empty string', () => {
    renderParams([{ mode: '' }, {}]);
    const [empty, inherited] = screen.getAllByLabelText('mode');
    expect(empty).toHaveAttribute('placeholder', '""');
    expect(inherited).toHaveAttribute('placeholder', 'manual');
  });

  it('removes one schedule with its overrides', () => {
    const { onChange } = renderParams([{ mode: 'y' }, { mode: 'z' }]);
    fireEvent.click(
      screen.getAllByRole('button', { name: 'Remove schedule' })[0]!
    );
    expect(onChange).toHaveBeenCalledWith(['0 6 * * *'], 0);
  });

  it('shows the fields of a schedule that was just added', () => {
    function Stateful(): ReactElement {
      const [expressions, setExpressions] = useState<string[]>([]);
      return (
        <CronLauncherPanel
          schedule={cronSchedule(expressions)}
          editable
          paramDefs={defaults}
          onChange={setExpressions}
          onClose={() => undefined}
        />
      );
    }
    render(<Stateful />);
    expect(screen.queryByLabelText('mode')).toBeNull();
    fireEvent.change(screen.getByLabelText('New cron expression'), {
      target: { value: '0 7 * * *' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add schedule' }));
    expect(screen.getByLabelText('mode')).toHaveAttribute(
      'placeholder',
      'manual'
    );
  });

  it('shows no fields for a workflow without parameters', () => {
    render(
      <CronLauncherPanel
        schedule={cronSchedule(['0 5 * * *'])}
        editable
        onChange={() => undefined}
        onClose={() => undefined}
      />
    );
    expect(screen.queryByRole('group', { name: 'Parameters' })).toBeNull();
  });
});

/** Picks the option of a native select by its text. */
function choose(select: HTMLElement, label: string) {
  const option = within(select).getByRole('option', { name: label });
  fireEvent.change(select, {
    target: { value: (option as HTMLOptionElement).value },
  });
}

describe('CronLauncherPanel typed overrides', () => {
  function renderTyped(
    paramDefs: Parameters<typeof CronLauncherPanel>[0]['paramDefs'],
    params: Record<string, string> = {}
  ) {
    const onParamChange = vi.fn();
    render(
      <CronLauncherPanel
        schedule={cronSchedule(['0 5 * * *'], [params])}
        editable
        paramDefs={paramDefs}
        onChange={() => undefined}
        onParamChange={onParamChange}
        onClose={() => undefined}
      />
    );
    return onParamChange;
  }

  // A list of values leaves no room for a typo, and its first option takes
  // the workflow default again.
  it('picks an allowed value or the default from a select', () => {
    const onParamChange = renderTyped([
      {
        name: 'mode',
        type: ParamDefType.string,
        required: false,
        enum: ['y', 'z'],
        default: 'y',
      },
    ]);
    const select = screen.getByLabelText('mode');
    expect(select).toHaveDisplayValue('Default (y)');
    choose(select, 'z');
    expect(onParamChange).toHaveBeenLastCalledWith(0, 'mode', 'z');
    choose(select, 'Default (y)');
    expect(onParamChange).toHaveBeenLastCalledWith(0, 'mode', null);
  });

  // The default option must not be taken for an allowed value.
  it('stores an allowed value that looks like an option name', () => {
    const onParamChange = renderTyped([
      {
        name: 'mode',
        type: ParamDefType.string,
        required: false,
        enum: ['__default__', ''],
      },
    ]);
    choose(screen.getByLabelText('mode'), '__default__');
    expect(onParamChange).toHaveBeenLastCalledWith(0, 'mode', '__default__');
  });

  it('writes a boolean override as a boolean', () => {
    const onParamChange = renderTyped([
      {
        name: 'dry_run',
        type: ParamDefType.boolean,
        required: false,
        default: false,
      },
    ]);
    const select = screen.getByLabelText('dry_run');
    expect(select).toHaveDisplayValue('Default (false)');
    choose(select, 'true');
    expect(onParamChange).toHaveBeenCalledWith(0, 'dry_run', true);
  });

  // A variable is resolved when the run starts, so only the server checks it.
  it('keeps a variable of a select param without an error', () => {
    renderTyped(
      [{ name: 'dry_run', type: ParamDefType.boolean, required: false }],
      { dry_run: '${FLAG}' }
    );
    expect(screen.getByLabelText('dry_run')).toHaveDisplayValue('${FLAG}');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('marks an override outside the allowed values', () => {
    renderTyped(
      [
        {
          name: 'mode',
          type: ParamDefType.string,
          required: false,
          enum: ['y', 'z'],
        },
      ],
      { mode: 'prd' }
    );
    expect(screen.getByLabelText('mode')).toHaveDisplayValue('prd');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Must be one of the allowed values'
    );
  });

  it('refuses an integer override that is not one', () => {
    const onParamChange = renderTyped([
      {
        name: 'count',
        type: ParamDefType.integer,
        required: false,
        minimum: 1,
      },
    ]);
    const field = screen.getByLabelText('count');
    fireEvent.change(field, { target: { value: 'x' } });
    fireEvent.blur(field);
    expect(onParamChange).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent('Must be an integer');
    fireEvent.change(field, { target: { value: '3' } });
    fireEvent.blur(field);
    expect(onParamChange).toHaveBeenCalledExactlyOnceWith(0, 'count', 3);
  });

  // A number is written as one whatever its spelling, unless it would lose
  // digits, or is not decimal, which the backend would read otherwise.
  it.each([
    [ParamDefType.number, '1.50', 1.5],
    [ParamDefType.number, '1e3', 1000],
    [ParamDefType.number, '0x10', '0x10'],
    [ParamDefType.number, '0.1', 0.1],
    [ParamDefType.number, '9007199254740993', '9007199254740993'],
    [ParamDefType.number, '1e400', '1e400'],
    [ParamDefType.integer, '01', 1],
    [ParamDefType.integer, '9007199254740993', '9007199254740993'],
  ])('writes the %s override %s as %j', (type, text, want) => {
    const onParamChange = renderTyped([
      { name: 'value', type, required: false },
    ]);
    const field = screen.getByLabelText('value');
    fireEvent.change(field, { target: { value: text } });
    fireEvent.blur(field);
    expect(onParamChange).toHaveBeenCalledWith(0, 'value', want);
  });

  // A variable is resolved when the run starts, so only the server checks it.
  it('sends a variable to a typed param unchecked', () => {
    const onParamChange = renderTyped([
      { name: 'count', type: ParamDefType.integer, required: false },
    ]);
    const field = screen.getByLabelText('count');
    fireEvent.change(field, { target: { value: '${COUNT}' } });
    fireEvent.blur(field);
    expect(onParamChange).toHaveBeenCalledWith(0, 'count', '${COUNT}');
  });
});

describe('CronLauncherPanel entries that are not plain cron', () => {
  const text = `schedule:
  - "0 6 * * *"
  - at: 2030-01-01T00:00:00Z
  - expression: "0 5 * * *"
    calendar: jp
    profile: prod
    params:
      mode: b
`;

  it('lists each entry with its overrides and edits them by entry', () => {
    const onParamChange = vi.fn();
    render(
      <CronLauncherPanel
        schedule={readSchedule(text)}
        editable
        paramDefs={[
          {
            name: 'mode',
            type: ParamDefType.string,
            required: false,
            default: 'manual',
          },
        ]}
        onChange={() => undefined}
        onParamChange={onParamChange}
        onClose={() => undefined}
      />
    );
    const rows = within(screen.getByLabelText('Schedules')).getAllByRole(
      'listitem'
    );
    expect(rows).toHaveLength(3);
    expect(rows[1]).toHaveTextContent('at 2030-01-01T00:00:00Z');
    expect(rows[2]).toHaveTextContent('0 5 * * * · jp (prod)');
    expect(within(rows[2]!).getByLabelText('mode')).toHaveValue('b');

    const field = within(rows[1]!).getByLabelText('mode');
    fireEvent.change(field, { target: { value: 'a' } });
    fireEvent.blur(field);
    expect(onParamChange).toHaveBeenCalledWith(1, 'mode', 'a');
    expect(
      screen.queryByText(/other schedule entries stay in the YAML/)
    ).toBeNull();
  });

  it('shows a server issue of the overrides on its entry', () => {
    render(
      <CronLauncherPanel
        schedule={readSchedule(text)}
        editable
        issues={[
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'invalid value for mode',
            fieldPath: 'schedule[2].params',
          },
        ]}
        onChange={() => undefined}
        onClose={() => undefined}
      />
    );
    const rows = within(screen.getByLabelText('Schedules')).getAllByRole(
      'listitem'
    );
    expect(within(rows[2]!).getByRole('alert')).toHaveTextContent(
      'invalid value for mode'
    );
    expect(within(rows[0]!).queryByRole('alert')).toBeNull();
  });
});

describe('CronLauncherPanel coinciding schedules', () => {
  const coincidences = [
    {
      scheduleIndex: 1,
      precedingScheduleIndex: 0,
      time: '2026-11-23T05:00:00+09:00',
      timezone: 'Asia/Tokyo',
    },
  ];
  const warning =
    'Does not start at Mon 2026-11-23 05:00 Asia/Tokyo: 0 * * * * · mode: x is due then too and takes precedence.';

  function renderCoinciding(
    schedules: Parameters<typeof CronLauncherPanel>[0]['schedules']
  ) {
    render(
      <CronLauncherPanel
        schedule={cronSchedule(
          ['0 * * * *', '0 5 * * *'],
          [{ mode: 'x' }, { mode: 'y' }]
        )}
        editable
        schedules={schedules}
        coincidences={coincidences}
        onChange={() => undefined}
        onClose={() => undefined}
      />
    );
    return within(screen.getByLabelText('Schedules')).getAllByRole('listitem');
  }

  it('warns on the schedule that another one takes precedence over', () => {
    const rows = renderCoinciding([
      {
        expression: '0 * * * *',
        kind: ScheduleKind.cron,
        paramOverrides: { mode: 'x' },
      },
      {
        expression: '0 5 * * *',
        kind: ScheduleKind.cron,
        paramOverrides: { mode: 'y' },
      },
    ]);
    expect(rows[1]).toHaveTextContent(warning);
    expect(rows[0]).not.toHaveTextContent('Does not start');
  });

  // A validation of an older text numbers other entries: its warning would
  // land on the wrong row.
  it('shows no warning while the server lists other entries', () => {
    const rows = renderCoinciding([
      { expression: '0 * * * *', kind: ScheduleKind.cron },
      { expression: '0 6 * * *', kind: ScheduleKind.cron },
    ]);
    expect(rows[1]).not.toHaveTextContent('Does not start');
  });

  // A plain cron entry whose profile was just removed still has one on the
  // server until the text is validated again.
  it('matches plain cron entries by their profile too', () => {
    const rows = renderCoinciding([
      {
        expression: '0 * * * *',
        kind: ScheduleKind.cron,
        paramOverrides: { mode: 'x' },
      },
      {
        expression: '0 5 * * *',
        kind: ScheduleKind.cron,
        profile: 'prod',
        paramOverrides: { mode: 'y' },
      },
    ]);
    expect(rows[1]).not.toHaveTextContent('Does not start');
  });

  // An entry that is not plain cron is matched by its timing and profile:
  // after an edit of its time, the warning of the old time must go.
  it('matches entries that are not plain cron by their timing', () => {
    const text = `schedule:
  - at: 2030-01-01T09:00:00+09:00
  - at: 2030-01-01T09:00:00+09:00
    profile: prod
`;
    const oneOffs = (second: string) => [
      { kind: ScheduleKind.at, at: '2030-01-01T00:00:00Z' },
      { kind: ScheduleKind.at, at: second, profile: 'prod' },
    ];
    const view = render(
      <CronLauncherPanel
        schedule={readSchedule(text)}
        editable
        schedules={oneOffs('2030-01-01T00:00:00Z')}
        coincidences={[
          { ...coincidences[0]!, time: '2030-01-01T09:00:00+09:00' },
        ]}
        onChange={() => undefined}
        onClose={() => undefined}
      />
    );
    expect(screen.getByText(/Does not start at/)).toBeInTheDocument();

    view.rerender(
      <CronLauncherPanel
        schedule={readSchedule(text)}
        editable
        schedules={oneOffs('2030-01-02T00:00:00Z')}
        coincidences={[
          { ...coincidences[0]!, time: '2030-01-01T09:00:00+09:00' },
        ]}
        onChange={() => undefined}
        onClose={() => undefined}
      />
    );
    expect(screen.queryByText(/Does not start at/)).toBeNull();
  });
});
