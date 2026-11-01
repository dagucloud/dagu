// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import {
  LoadErrorState,
  NotFoundState,
  PageLoading,
} from '@/components/PageStates';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanWriteCalendars } from '@/contexts/AuthContext';
import { CalendarChangesSummary } from '@/features/calendars/components/CalendarChangesSummary';
import { CalendarDependents } from '@/features/calendars/components/CalendarDependents';
import { CalendarYearView } from '@/features/calendars/components/CalendarYearView';
import { useCalendarPreview } from '@/features/calendars/hooks/useCalendarPreview';
import { useCalendarSchema } from '@/features/calendars/hooks/useCalendarSchema';
import {
  calendarsPath,
  useFollowAppBarRemoteNode,
  useScopedRemoteNode,
} from '@/features/calendars/navigation';
import { parseValidationMarkers } from '@/features/dags/components/dag-editor/validationMarkers';
import { useClient, useQuery } from '@/hooks/api';
import { useContentEditor } from '@/hooks/useContentEditor';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { applyCalendarNameExamples } from '@/lib/calendarNameExamples';
import dayjs from '@/lib/dayjs';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import {
  AlertTriangle,
  ArrowLeft,
  RotateCcw,
  Save,
  Trash2,
} from 'lucide-react';
import React, {
  Suspense,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';

const DAGEditor = React.lazy(
  () => import('@/features/dags/components/dag-editor/DAGEditor')
);

/**
 * Shows one business calendar: its file in an editor beside a live year of
 * its business days, and what depends on it.
 */
function CalendarPage(): React.ReactNode {
  const { name = '' } = useParams();
  const appBarContext = useContext(AppBarContext);
  useFollowAppBarRemoteNode();
  const remoteNode = useScopedRemoteNode();
  const client = useClient();
  const navigate = useNavigate();
  const { ts, locale } = useI18n();
  const dayjsLocale = locale === 'zh-CN' ? 'zh-cn' : locale;
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const canWrite = useCanWriteCalendars();
  const [year, setYear] = useState(() => dayjs().year());
  const [isDeleting, setIsDeleting] = useState(false);
  const saveRef = useRef<(() => Promise<void>) | null>(null);
  const editorKey = `${remoteNode}:${name}`;
  // Advances on each visit of a node and calendar, and on each save. A
  // response sent under an earlier generation must not change what the page
  // shows: the user moved on, or a later save superseded it.
  const generationRef = useRef(0);
  useEffect(() => {
    generationRef.current += 1;
  }, [editorKey]);

  useEffect(() => {
    appBarContext.setTitle('Calendars');
  }, [appBarContext]);

  const { data, error, mutate } = useQuery(
    '/calendars/{name}',
    { params: { path: { name }, query: { remoteNode } } },
    { revalidateOnFocus: true }
  );
  const { data: list } = useQuery('/calendars', {
    params: { query: { remoteNode } },
  });

  const builtin = !!data?.builtin;
  const editable = canWrite && !!data && !builtin;
  const editor = useContentEditor({
    key: editorKey,
    serverContent: data ? data.spec : null,
  });
  const content = editable ? editor.currentValue : (data?.spec ?? null);
  const { preview, isPreviewing, failed, retry } = useCalendarPreview({
    name,
    remoteNode,
    spec: content,
    baseline: data?.spec ?? null,
    year,
  });
  const problems = preview?.errors ?? data?.errors ?? [];
  const { markers, unpositioned } = useMemo(
    () => parseValidationMarkers(problems),
    [problems]
  );

  const baseSchema = useCalendarSchema();
  const schema = useMemo(() => {
    if (!baseSchema) {
      return null;
    }
    const includable = (list?.calendars ?? [])
      .filter((calendar) => !calendar.builtin && calendar.name !== name)
      .map((calendar) => calendar.name);
    const augmented = structuredClone(baseSchema);
    applyCalendarNameExamples(augmented, includable);
    return augmented;
  }, [baseSchema, list, name]);

  const handleSave = useCallback(async () => {
    const value = editor.currentValue;
    if (!editable || value === null || !editor.hasUnsavedChanges) {
      return;
    }
    const generation = ++generationRef.current;
    editor.beginSave(value);
    // A request that throws, as on a lost connection, fails like one that
    // returns an error.
    const response = await client
      .PUT('/calendars/{name}', {
        params: { path: { name }, query: { remoteNode } },
        body: { spec: value },
      })
      .catch(() => null);
    if (generationRef.current !== generation) {
      return;
    }
    const result = response?.data;
    if (!result) {
      editor.cancelSave();
      showError(response?.error?.message || ts('Failed to save the calendar.'));
      return;
    }
    if (result.errors.length > 0) {
      editor.cancelSave();
      showError(
        ts('The calendar was not saved'),
        undefined,
        ts('Problems'),
        result.errors
      );
      return;
    }
    editor.markAsSaved(value);
    void mutate();
    showToast(ts('Calendar saved'));
  }, [
    client,
    editable,
    editor,
    mutate,
    name,
    remoteNode,
    showError,
    showToast,
    ts,
  ]);

  useEffect(() => {
    saveRef.current = handleSave;
  }, [handleSave]);

  useEffect(() => {
    if (!editable) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key === 's') {
        event.preventDefault();
        void saveRef.current?.();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [editable]);

  async function handleDelete(): Promise<void> {
    const generation = generationRef.current;
    const response = await client
      .DELETE('/calendars/{name}', {
        params: { path: { name }, query: { remoteNode } },
      })
      .catch(() => null);
    setIsDeleting(false);
    if (generationRef.current !== generation) {
      return;
    }
    if (!response || response.error) {
      showError(
        response?.error?.message || ts('Failed to delete the calendar.')
      );
      void mutate();
      return;
    }
    showToast(ts('Calendar {name} deleted', { name }));
    navigate(calendarsPath(remoteNode));
  }

  if (error && !data) {
    return isNotFoundQueryError(error) ? (
      <NotFoundState
        title={<I18nText text={'Calendar not found'} />}
        description={
          <I18nText
            text={'No calendar is named {name}. It may have been deleted.'}
            values={{ name }}
          />
        }
        action={{
          to: calendarsPath(remoteNode),
          label: <I18nText text={'Back to calendars'} />,
        }}
      />
    ) : (
      <LoadErrorState
        title={<I18nText text={"Couldn't load the calendar"} />}
        error={error}
        onRetry={() => void mutate()}
      />
    );
  }
  if (!data) {
    return <PageLoading />;
  }

  const inUse = data.usedBy.length > 0 || data.includedBy.length > 0;
  const status = isPreviewing
    ? ts('Checking...')
    : failed
      ? ts("Couldn't check")
      : problems.length > 0
        ? ts(problems.length === 1 ? '{count} problem' : '{count} problems', {
            count: problems.length,
          })
        : ts('Valid');

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-64">
          <Button variant="ghost" size="xs" asChild className="-ml-2">
            <Link to={calendarsPath(remoteNode)}>
              <ArrowLeft className="h-3.5 w-3.5" />
              <I18nText text={'Calendars'} />
            </Link>
          </Button>
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="break-words text-2xl font-semibold tracking-[-0.01em]">
              {data.name}
            </h1>
            {builtin && (
              <Badge variant="outline">
                <I18nText text={'Built in'} />
              </Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            {data.description}
            {data.through && (
              <>
                {data.description ? ' · ' : ''}
                <I18nText
                  text={'Holidays through {date}'}
                  values={{ date: data.through }}
                />
              </>
            )}
          </p>
        </div>
        {editable && (
          <div className="flex items-center gap-2">
            <I18nProps>
              <Button
                variant="outline"
                size="sm"
                disabled={inUse}
                title={
                  inUse
                    ? 'Remove the schedules and includes that use this calendar first'
                    : 'Delete this calendar'
                }
                onClick={() => setIsDeleting(true)}
              >
                <Trash2 className="h-4 w-4" />
                <I18nText text={'Delete'} />
              </Button>
            </I18nProps>
            <I18nProps>
              <Button
                variant="outline"
                size="sm"
                title="Revert to last saved version"
                disabled={!editor.hasUnsavedChanges}
                onClick={editor.discardChanges}
              >
                <RotateCcw className="h-4 w-4" />
                <I18nText text={'Revert'} />
              </Button>
            </I18nProps>
            <I18nProps>
              <Button
                variant="primary"
                size="sm"
                title="Save changes (Ctrl+S / Cmd+S)"
                disabled={!editor.hasUnsavedChanges}
                onClick={() => void handleSave()}
              >
                <Save className="h-4 w-4" />
                <I18nText text={'Save'} />
              </Button>
            </I18nProps>
          </div>
        )}
      </div>

      {editor.conflict.hasConflict && (
        <div
          role="alert"
          className="flex flex-wrap items-center gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm"
        >
          <AlertTriangle
            className="h-4 w-4 text-amber-600"
            aria-hidden="true"
          />
          <I18nText
            text={
              'The calendar file changed on the server while you were editing.'
            }
          />
          <Button
            variant="outline"
            size="xs"
            onClick={() => editor.resolveConflict('discard')}
          >
            <I18nText text={'Load the server version'} />
          </Button>
          <Button
            variant="ghost"
            size="xs"
            onClick={() => editor.resolveConflict('ignore')}
          >
            <I18nText text={'Keep my edits'} />
          </Button>
        </div>
      )}
      {editor.hasUnsavedChanges && preview && (
        <CalendarChangesSummary
          changes={preview.changes}
          dagCount={data.usedBy.length}
          locale={dayjsLocale}
        />
      )}
      {(preview?.warnings ?? data.warnings).map((warning) => (
        <p
          key={warning}
          role="status"
          className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-900 dark:text-amber-100"
        >
          {warning}
        </p>
      ))}

      <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[minmax(0,5fr)_minmax(0,6fr)]">
        {builtin ? (
          <section className="card-obsidian p-3 text-sm text-muted-foreground">
            <I18nText
              text={
                'This calendar is built in: Monday to Friday, with no holidays. It cannot be changed. Create a calendar file to list holidays.'
              }
            />
          </section>
        ) : (
          <section className="flex min-h-[420px] flex-col gap-2">
            <div className="flex items-center justify-between text-xs text-muted-foreground">
              <code>{data.name}.yaml</code>
              <span aria-live="polite">{status}</span>
            </div>
            <div className="min-h-[360px] flex-1 overflow-hidden rounded-md border">
              {schema === null || content === null ? (
                <PageLoading />
              ) : (
                <Suspense fallback={<PageLoading />}>
                  <DAGEditor
                    value={content}
                    readOnly={!editable}
                    onChange={
                      editable
                        ? (value) => editor.setCurrentValue(value ?? '')
                        : undefined
                    }
                    className="h-full"
                    modelUri={`inmemory://dagu/${remoteNode}/calendars/${data.name}.yaml`}
                    schema={schema}
                    markers={markers}
                  />
                </Suspense>
              )}
            </div>
            {unpositioned.map((problem) => (
              <p
                key={problem}
                className="whitespace-pre-wrap break-words rounded-md bg-destructive/10 p-2 font-mono text-xs text-destructive"
              >
                {problem}
              </p>
            ))}
          </section>
        )}
        <section className="card-obsidian min-h-0 overflow-auto p-3">
          <CalendarYearView
            preview={preview}
            year={year}
            failed={failed}
            onRetry={retry}
            onYearChange={setYear}
            locale={dayjsLocale}
          />
        </section>
      </div>

      <section className="card-obsidian p-3">
        <CalendarDependents calendar={data} remoteNode={remoteNode} />
      </section>

      <I18nProps>
        <ConfirmModal
          title="Delete calendar"
          buttonText="Delete"
          visible={isDeleting}
          dismissModal={() => setIsDeleting(false)}
          onSubmit={() => void handleDelete()}
        >
          <span className="text-sm text-muted-foreground">
            <I18nText text="Delete {name}?" values={{ name: data.name }} />
          </span>
        </ConfirmModal>
      </I18nProps>
    </div>
  );
}

export default CalendarPage;
