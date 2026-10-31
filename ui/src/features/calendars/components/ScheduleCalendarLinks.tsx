// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import React, { type ReactElement } from 'react';
import { Link } from 'react-router-dom';
import { calendarPath, useScopedRemoteNode } from '../navigation';

type Schedule = components['schemas']['Schedule'];

/** Links the calendars that schedules fire on to their pages. */
export function ScheduleCalendarLinks({
  schedules,
}: {
  schedules: Schedule[];
}): ReactElement | null {
  const names = [
    ...new Set(
      schedules.flatMap((schedule) =>
        schedule.calendar ? [schedule.calendar] : []
      )
    ),
  ];
  if (names.length === 0) {
    return null;
  }
  return <CalendarLinkList names={names} />;
}

function CalendarLinkList({ names }: { names: string[] }): ReactElement {
  const { ts } = useI18n();
  const remoteNode = useScopedRemoteNode();
  return (
    <p className="text-sm text-muted-foreground">
      <span className="font-medium text-foreground">{ts('Calendar:')}</span>{' '}
      {names.map((name, index) => (
        <React.Fragment key={name}>
          {index > 0 && ', '}
          <Link
            to={calendarPath(name, remoteNode)}
            className="text-primary hover:underline"
          >
            {name}
          </Link>
        </React.Fragment>
      ))}
    </p>
  );
}
