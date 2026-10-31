// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { I18nText } from '@/i18n/I18nText';
import React from 'react';
import { Link } from 'react-router-dom';

type CalendarDetails = components['schemas']['CalendarDetails'];

/**
 * Lists what a calendar is connected to: the DAGs that use it, the
 * calendars that include it, and the calendars it includes.
 */
export function CalendarDependents({
  calendar,
}: {
  calendar: CalendarDetails;
}): React.ReactElement {
  return (
    <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[auto_1fr]">
      <dt className="text-muted-foreground">
        <I18nText text={'Used by'} />
      </dt>
      <dd>
        {calendar.usedBy.length === 0 ? (
          <span className="text-muted-foreground">
            <I18nText text={'No DAG schedules use this calendar.'} />
          </span>
        ) : (
          <ul className="flex flex-wrap gap-x-4 gap-y-1">
            {calendar.usedBy.map((user) => (
              <li key={user.fileName}>
                <Link
                  to={`/dags/${encodeURIComponent(user.fileName)}`}
                  className="text-primary hover:underline"
                >
                  {user.name}
                </Link>
                {user.workspace && (
                  <span className="text-muted-foreground">
                    {' '}
                    · {user.workspace}
                  </span>
                )}
                {user.via && (
                  <span className="text-muted-foreground">
                    {' '}
                    (
                    <I18nText
                      text={'via {calendar}'}
                      values={{ calendar: user.via }}
                    />
                    )
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </dd>
      {calendar.includedBy.length > 0 && (
        <>
          <dt className="text-muted-foreground">
            <I18nText text={'Included by'} />
          </dt>
          <dd>
            <CalendarLinks names={calendar.includedBy} />
          </dd>
        </>
      )}
      {calendar.includes.length > 0 && (
        <>
          <dt className="text-muted-foreground">
            <I18nText text={'Includes'} />
          </dt>
          <dd>
            <CalendarLinks names={calendar.includes} />
          </dd>
        </>
      )}
    </dl>
  );
}

function CalendarLinks({ names }: { names: string[] }): React.ReactElement {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1">
      {names.map((name) => (
        <li key={name}>
          <Link
            to={`/calendars/${encodeURIComponent(name)}`}
            className="text-primary hover:underline"
          >
            {name}
          </Link>
        </li>
      ))}
    </ul>
  );
}
