// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { InlineLoadError } from '@/components/PageStates';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanWriteCalendars } from '@/contexts/AuthContext';
import { NewCalendarDialog } from '@/features/calendars/components/NewCalendarDialog';
import {
  calendarPath,
  useScopedRemoteNode,
} from '@/features/calendars/navigation';
import { useQuery } from '@/hooks/api';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { CalendarDays, Plus } from 'lucide-react';
import React, { useContext, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';

const COLUMN_COUNT = 5;

/** Lists business calendars with what needs attention and who uses them. */
function CalendarsPage(): React.ReactElement {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = useScopedRemoteNode();
  const canWrite = useCanWriteCalendars();
  const navigate = useNavigate();
  const [isCreating, setIsCreating] = useState(false);

  useEffect(() => {
    appBarContext.setTitle('Calendars');
  }, [appBarContext]);

  const { data, error, mutate, isLoading } = useQuery(
    '/calendars',
    { params: { query: { remoteNode, includeUsage: true } } },
    { revalidateOnFocus: true }
  );
  const calendars = data?.calendars ?? [];
  const fileNames = calendars
    .filter((calendar) => !calendar.builtin)
    .map((calendar) => calendar.name);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-64">
          <h1 className="text-2xl font-semibold tracking-[-0.01em]">
            <I18nText text={'Calendars'} />
          </h1>
          <p className="text-sm text-muted-foreground">
            <I18nTemplate
              text="Business days that start schedules fire on. A schedule names a calendar with {key}."
              values={{ key: <code className="text-xs">calendar</code> }}
            />
          </p>
        </div>
        {canWrite && (
          <Button
            size="sm"
            variant="primary"
            onClick={() => setIsCreating(true)}
          >
            <Plus className="h-4 w-4" />
            <I18nText text={'New calendar'} />
          </Button>
        )}
      </div>

      <div className="card-obsidian min-h-0 flex-1 !overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[240px]">
                <I18nText text={'Name'} />
              </TableHead>
              <TableHead>
                <I18nText text={'Description'} />
              </TableHead>
              <TableHead className="w-[140px]">
                <I18nText text={'Holidays through'} />
              </TableHead>
              <TableHead className="w-[160px]">
                <I18nText text={'Status'} />
              </TableHead>
              <TableHead className="w-[80px] text-right">
                <I18nText text={'DAGs'} />
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {error && !data ? (
              <TableRow>
                <TableCell colSpan={COLUMN_COUNT} className="py-8">
                  <InlineLoadError
                    error={error}
                    onRetry={() => void mutate()}
                  />
                </TableCell>
              </TableRow>
            ) : isLoading && !data ? (
              <TableRow>
                <TableCell
                  colSpan={COLUMN_COUNT}
                  className="py-8 text-center text-muted-foreground"
                >
                  <I18nText text={'Loading calendars...'} />
                </TableCell>
              </TableRow>
            ) : (
              calendars.map((calendar) => (
                <TableRow key={`${calendar.name}:${calendar.builtin}`}>
                  <TableCell>
                    <div className="flex min-w-0 items-center gap-2">
                      <CalendarDays className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                      {calendar.error &&
                      !calendar.builtin &&
                      fileShadowsBuiltin(calendar.name) ? (
                        <code className="break-words text-xs">
                          {calendar.name}
                        </code>
                      ) : (
                        <Link
                          to={calendarPath(calendar.name, remoteNode)}
                          className="break-words font-medium text-primary hover:underline"
                        >
                          {calendar.name}
                        </Link>
                      )}
                    </div>
                    {calendar.error && (
                      <p className="mt-1 whitespace-normal break-words text-destructive">
                        {calendar.error}
                      </p>
                    )}
                  </TableCell>
                  <TableCell className="whitespace-normal break-words text-muted-foreground">
                    {calendar.description}
                  </TableCell>
                  <TableCell className="tabular-nums">
                    {calendar.through ?? (
                      <span className="text-muted-foreground">-</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <CalendarStatus calendar={calendar} />
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {calendar.dagCount ?? ''}
                  </TableCell>
                </TableRow>
              ))
            )}
            {data && fileNames.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={COLUMN_COUNT}
                  className="whitespace-normal py-6 text-center text-muted-foreground"
                >
                  <I18nTemplate
                    text="No calendar files yet. Create one here, or add {file} to paths.calendars_dir."
                    values={{
                      file: <code className="text-xs">name.yaml</code>,
                    }}
                  />
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <NewCalendarDialog
        open={isCreating}
        remoteNode={remoteNode}
        existingNames={fileNames}
        onClose={() => setIsCreating(false)}
        onCreated={(name) => {
          setIsCreating(false);
          void mutate();
          navigate(calendarPath(name, remoteNode));
        }}
      />
    </div>
  );
}

type CalendarSummary = components['schemas']['CalendarSummary'];

function CalendarStatus({
  calendar,
}: {
  calendar: CalendarSummary;
}): React.ReactElement | null {
  if (calendar.builtin) {
    return (
      <Badge variant="outline">
        <I18nText text={'Built in'} />
      </Badge>
    );
  }
  if (calendar.error) {
    return (
      <Badge variant="error">
        <I18nText text={'Invalid'} />
      </Badge>
    );
  }
  if (calendar.warning) {
    return (
      <Badge variant="warning" title={calendar.warning}>
        <I18nText text={'Holidays run out'} />
      </Badge>
    );
  }
  return null;
}

// A file named after a built-in calendar never loads; the name opens the
// built-in calendar instead.
function fileShadowsBuiltin(name: string): boolean {
  return name === 'weekdays' || name === 'none';
}

export default CalendarsPage;
