// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ArrowLeft,
  Download,
  FileCode,
  Pencil,
  Save,
  Trash2,
  Undo2,
} from 'lucide-react';
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
  type ReactElement,
} from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { SkillIssueSeverity } from '@/api/v1/schema';
import {
  LoadErrorState,
  NotFoundState,
  PageLoading,
} from '@/components/PageStates';
import { Button } from '@/components/ui/button';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { IconButton } from '@/components/ui/icon-button';
import { Input } from '@/components/ui/input';
import { WikiPageMarkdownPreview } from '@/components/ui/wiki-page-markdown-preview';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { editorNotices, SKILLS_PATH } from '@/features/skills/skill';
import { formatBytes, languageForPath } from '@/features/skills/skillFiles';
import {
  readSkillForm,
  setSkillBody,
  setSkillKey,
  type SkillEditOutcome,
} from '@/features/skills/skillForm';
import {
  SKILL_FILE,
  sortIssues,
  visibleIssues,
  type SkillFile,
  type SkillIssue,
} from '@/features/skills/skillIssues';
import {
  SkillIssuePanel,
  SkillStateBadge,
} from '@/features/skills/SkillIssueViews';
import { SkillMeter } from '@/features/skills/SkillMeter';
import {
  downloadSkillZip,
  INITIAL_SYNC,
  readSkillFileText,
  removeSkill,
  removeSkillFile,
  syncReducer,
  writeHash,
  writeSkillFile,
  writeSkillText,
  readSkillFileBlob,
  type WriteOutcome,
} from '@/features/skills/skillSync';
import { SkillUsage } from '@/features/skills/SkillUsage';
import { useLeaveGuardLinks } from '@/features/workflow-workspace/settings/useLeaveGuardLinks';
import { useSettingsLeaveGuard } from '@/features/workflow-workspace/settings/useSettingsLeaveGuard';
import { useClient, useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import { cn } from '@/lib/utils';
import { SkillBinaryView } from './SkillBinaryView';
import { SkillConflictBanner } from './SkillConflictBanner';
import { SkillFileDialog, type FileDialogMode } from './SkillFileDialog';
import { SkillFileTree } from './SkillFileTree';

// Monaco is large: the list of skills never loads it.
const MarkdownEditor = lazy(
  () => import('@/components/editors/MarkdownEditor')
);

// No path is inside this base: a click on any link asks before it drops a draft.
const NO_BASE = '\0';

type Mode = 'edit' | 'preview';

/** A copy of the record without one key. */
function without<T>(record: Record<string, T>, key: string): Record<string, T> {
  const next = { ...record };
  delete next[key];
  return next;
}

interface Listing {
  errors: SkillIssue[];
  warnings: SkillIssue[];
  files: SkillFile[];
}

/**
 * The editor of one skill: its state, its files and the Markdown of the file
 * that is open. SKILL.md has the description above the body. Every write
 * carries the hash that the editor read and chains the hash that the server
 * gives back. A conflict keeps the draft.
 */
export function SkillEditor({
  name,
  remoteNode,
  canWrite,
  narrow,
  onChanged,
}: {
  name: string;
  remoteNode: string;
  canWrite: boolean;
  /** The editor has the full width: it shows a link back to the list. */
  narrow: boolean;
  /** Called after the skill changed on the server: save, file change or delete. */
  onChanged(): void;
}): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const navigate = useNavigate();
  const target = useMemo(() => ({ name, remoteNode }), [name, remoteNode]);

  const {
    data,
    error: loadError,
    mutate,
  } = useQuery(
    '/skills/{name}',
    { params: { path: { name }, query: { remoteNode } } },
    { revalidateOnFocus: false, revalidateOnMount: true }
  );

  const [sync, dispatch] = useReducer(syncReducer, INITIAL_SYNC);
  const [active, setActive] = useState(SKILL_FILE);
  const [mode, setMode] = useState<Mode>('edit');
  const [wholeFile, setWholeFile] = useState(false);
  // The unsaved text of each file that the user changed.
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  // The text that this editor wrote, and the text that it read from files.
  const [written, setWritten] = useState<Record<string, string>>({});
  const [fetched, setFetched] = useState<
    Record<string, { sha: string; text: string }>
  >({});
  const [fileProblems, setFileProblems] = useState<Record<string, string>>({});
  // What the last write answered: newer than `data` until it reloads.
  const [latest, setLatest] = useState<Listing | null>(null);
  const [refused, setRefused] = useState<SkillIssue[]>([]);
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [dialog, setDialog] = useState<FileDialogMode | null>(null);
  const [confirming, setConfirming] = useState<'skill' | 'file' | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const files = useMemo<SkillFile[]>(
    () => latest?.files ?? data?.files ?? [],
    [latest, data?.files]
  );
  const serverIssues = useMemo<SkillIssue[]>(
    () => [
      ...(latest?.errors ?? data?.errors ?? []),
      ...(latest?.warnings ?? data?.warnings ?? []),
    ],
    [latest, data?.errors, data?.warnings]
  );

  // A fresh answer of the server replaces the answer of the last write.
  const loadedHash = data?.hash;
  useEffect(() => {
    setLatest(null);
  }, [loadedHash]);

  const activeFile = files.find((file) => file.path === active);
  useEffect(() => {
    if (data && active !== SKILL_FILE && files.length > 0 && !activeFile) {
      setActive(SKILL_FILE);
    }
  }, [data, active, files.length, activeFile]);

  const savedText = useCallback(
    (path: string): string | undefined =>
      written[path] ??
      (path === SKILL_FILE ? data?.content : fetched[path]?.text),
    [written, fetched, data?.content]
  );

  // Open a text file: read it once, and again when the server changed it.
  const needsFetch =
    active !== SKILL_FILE &&
    !!activeFile?.text &&
    written[active] === undefined &&
    fetched[active]?.sha !== activeFile.sha256 &&
    fileProblems[active] === undefined;
  useEffect(() => {
    if (!needsFetch || !activeFile) return;
    const controller = new AbortController();
    const { path, sha256 } = activeFile;
    readSkillFileText(client, target, path, controller.signal)
      .then((text) =>
        setFetched((previous) => ({
          ...previous,
          [path]: { sha: sha256, text },
        }))
      )
      .catch((failure: unknown) => {
        if (controller.signal.aborted) return;
        setFileProblems((previous) => ({
          ...previous,
          [path]:
            failure instanceof Error
              ? failure.message
              : ts('The file could not be read.'),
        }));
      });
    return () => controller.abort();
  }, [needsFetch, activeFile, client, target, ts]);

  const dirtyPaths = useMemo(
    () =>
      new Set(
        Object.keys(drafts).filter((path) => drafts[path] !== savedText(path))
      ),
    [drafts, savedText]
  );
  const dirty = dirtyPaths.size > 0;
  const activeDirty = dirtyPaths.has(active);

  const skillText = drafts[SKILL_FILE] ?? savedText(SKILL_FILE) ?? '';
  const parsed = useMemo(() => readSkillForm(skillText), [skillText]);
  const formWhole = wholeFile || !parsed.ok || parsed.form.unknown.length > 0;

  const { setDirty, guard, confirmDialog } = useSettingsLeaveGuard();
  useEffect(() => {
    setDirty('page', dirty);
    return () => setDirty('page', false);
  }, [dirty, setDirty]);
  useLeaveGuardLinks({ dirty, baseHref: NO_BASE, guard });

  const hash = writeHash(sync, data?.hash);
  const hashRef = useRef(hash);
  hashRef.current = hash;
  const conflicted = sync.conflict !== null;
  const canSave = canWrite && !busy && activeDirty && !conflicted;

  /**
   * Takes the answer of a write. A success chains the hash. A conflict keeps
   * the draft and remembers the hash of the server. A refusal shows its rules.
   */
  const settle = useCallback(
    (outcome: WriteOutcome): outcome is Extract<WriteOutcome, { ok: true }> => {
      if (outcome.ok) {
        dispatch({ type: 'wrote', hash: outcome.result.hash });
        setLatest({
          errors: outcome.result.errors,
          warnings: outcome.result.warnings,
          files: outcome.result.files,
        });
        setRefused([]);
        setProblem(null);
        return true;
      }
      if (outcome.kind === 'conflict') {
        dispatch({ type: 'conflict', hash: outcome.hash });
        setProblem(null);
      } else if (outcome.kind === 'invalid') {
        // The panel lists the rules. The message stands alone only without them.
        setRefused(outcome.issues);
        setProblem(outcome.issues.length > 0 ? null : outcome.message);
      } else {
        setProblem(outcome.message);
      }
      return false;
    },
    []
  );

  const refresh = useCallback(async () => {
    await mutate();
    onChanged();
  }, [mutate, onChanged]);

  const save = useCallback(async () => {
    if (!canSave) return;
    const path = active;
    const text = drafts[path] ?? '';
    setBusy(true);
    setProblem(null);
    const outcome =
      path === SKILL_FILE
        ? await writeSkillText(
            client,
            target,
            text,
            { hash },
            ts('The skill was not saved.')
          )
        : await writeSkillFile(
            client,
            target,
            path,
            text,
            hash,
            ts('The skill was not saved.')
          );
    if (settle(outcome)) {
      setWritten((previous) => ({ ...previous, [path]: text }));
      // The text that the user typed during the save stays a draft.
      setDrafts((previous) =>
        previous[path] === text ? without(previous, path) : previous
      );
      await refresh();
    }
    setBusy(false);
  }, [canSave, active, drafts, client, target, hash, ts, settle, refresh]);

  // Ctrl+S and Cmd+S save. The editor lets the key reach the page.
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    if (!canWrite) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        event.preventDefault();
        void saveRef.current();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [canWrite]);

  const reload = async () => {
    setBusy(true);
    await mutate();
    setDrafts({});
    setWritten({});
    setFetched({});
    setFileProblems({});
    setLatest(null);
    setRefused([]);
    setProblem(null);
    dispatch({ type: 'reset' });
    onChanged();
    setBusy(false);
  };

  const removeSkillFolder = async () => {
    setBusy(true);
    setDeleteError(null);
    const outcome = await removeSkill(
      client,
      target,
      hash,
      ts('The skill was not deleted.')
    );
    setBusy(false);
    if (outcome.ok) {
      onChanged();
      navigate(SKILLS_PATH, { replace: true });
      return;
    }
    if (outcome.kind === 'conflict') {
      dispatch({ type: 'conflict', hash: outcome.hash });
      setConfirming(null);
      return;
    }
    setDeleteError(outcome.message);
  };

  const removeActiveFile = async () => {
    const path = active;
    setBusy(true);
    setDeleteError(null);
    const outcome = await removeSkillFile(
      client,
      target,
      path,
      hash,
      ts('The file was not deleted.')
    );
    if (outcome.ok) {
      settle(outcome);
      setDrafts((previous) => without(previous, path));
      setActive(SKILL_FILE);
      setConfirming(null);
      await refresh();
    } else if (outcome.kind === 'conflict') {
      settle(outcome);
      setConfirming(null);
    } else {
      setDeleteError(outcome.message);
    }
    setBusy(false);
  };

  // Adds a file, from the dialog of a new text file or of an upload.
  const addFile = async (path: string, file: File | null) => {
    setBusy(true);
    const outcome = await writeSkillFile(
      client,
      target,
      path,
      file ?? '',
      hashRef.current,
      ts('The file was not saved.')
    );
    setBusy(false);
    if (outcome.ok) {
      settle(outcome);
      if (!file) setWritten((previous) => ({ ...previous, [path]: '' }));
      setActive(path);
      setDialog(null);
      await refresh();
      return null;
    }
    if (outcome.kind === 'conflict') {
      settle(outcome);
      setDialog(null);
      return null;
    }
    return outcome.message;
  };

  // Renames a file: write the new path, then delete the old one with the new
  // hash. If the second call fails, the new file exists: the message says so.
  const renameFile = async (to: string) => {
    const from = active;
    const source = files.find((file) => file.path === from);
    if (!source) return ts('The file was not saved.');
    setBusy(true);
    try {
      let content: string | Blob;
      try {
        content = source.text
          ? (drafts[from] ??
            savedText(from) ??
            (await readSkillFileText(client, target, from)))
          : await readSkillFileBlob(client, target, from);
      } catch (failure) {
        return failure instanceof Error
          ? failure.message
          : ts('The file could not be read.');
      }
      const created = await writeSkillFile(
        client,
        target,
        to,
        content,
        hashRef.current,
        ts('The file was not saved.')
      );
      if (!created.ok) {
        if (created.kind === 'conflict') {
          settle(created);
          setDialog(null);
          return null;
        }
        return created.message;
      }
      settle(created);
      if (typeof content === 'string') {
        setWritten((previous) => ({ ...previous, [to]: content as string }));
      }
      setActive(to);
      const removed = await removeSkillFile(
        client,
        target,
        from,
        created.result.hash,
        ts('The file was not deleted.')
      );
      if (!removed.ok) {
        setDialog(null);
        setProblem(
          ts(
            'The new file {to} exists, but the old file {from} was not deleted: {message} Delete {from} by hand, or restore it.',
            { to, from, message: removed.message }
          )
        );
        if (removed.kind === 'conflict') {
          dispatch({ type: 'conflict', hash: removed.hash });
        }
        await refresh();
        return null;
      }
      settle(removed);
      setDrafts((previous) => without(previous, from));
      setWritten((previous) => without(previous, from));
      setDialog(null);
      await refresh();
      return null;
    } finally {
      setBusy(false);
    }
  };

  const submitDialog = async (path: string, file: File | null) =>
    dialog === 'rename' ? renameFile(path) : addFile(path, file);

  const download = async () => {
    setDownloading(true);
    setProblem(null);
    try {
      await downloadSkillZip(client, remoteNode, [name]);
    } catch (failure) {
      setProblem(
        failure instanceof Error ? failure.message : ts('The download failed.')
      );
    } finally {
      setDownloading(false);
    }
  };

  if (!data) {
    if (loadError && isNotFoundQueryError(loadError)) {
      return (
        <NotFoundState
          title={ts('Skill not found')}
          description={ts('No skill has the name "{name}".', { name })}
          action={{ to: SKILLS_PATH, label: ts('Back to the skills') }}
        />
      );
    }
    if (loadError) {
      return (
        <LoadErrorState
          title={ts("Couldn't load the skill")}
          error={loadError}
          onRetry={() => void mutate()}
        />
      );
    }
    return <PageLoading />;
  }

  const setDraft = (path: string, text: string) => {
    setDrafts((previous) => ({ ...previous, [path]: text }));
    setProblem(null);
    setRefused([]);
  };
  const apply = (outcome: SkillEditOutcome) => {
    if (outcome.ok) setDraft(SKILL_FILE, outcome.text);
  };
  const onText = (next: string | undefined) => {
    if (active !== SKILL_FILE) setDraft(active, next ?? '');
    else if (formWhole) setDraft(SKILL_FILE, next ?? '');
    else apply(setSkillBody(skillText, next ?? ''));
  };
  const revert = () => {
    setDrafts((previous) => without(previous, active));
    setProblem(null);
    setRefused([]);
  };

  const issues = visibleIssues(
    serverIssues,
    dirtyPaths.has(SKILL_FILE) ? { name, text: skillText } : null
  );
  const state = {
    errors: issues.filter(
      (issue) => issue.severity === SkillIssueSeverity.error
    ),
    warnings: issues.filter(
      (issue) => issue.severity !== SkillIssueSeverity.error
    ),
  };

  const notices = editorNotices({ saveError: problem });

  const onSkillMd = active === SKILL_FILE;
  const description = parsed.ok ? parsed.form.description : '';
  const text = onSkillMd
    ? formWhole
      ? skillText
      : parsed.ok
        ? parsed.form.body
        : skillText
    : (drafts[active] ?? savedText(active) ?? '');
  const language = languageForPath(active);
  const isMarkdown = language === 'markdown';
  const textFile = onSkillMd || !!activeFile?.text;
  const loadingText = textFile && savedText(active) === undefined;
  const readFailure = fileProblems[active];

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 overflow-y-auto lg:overflow-visible [&>*]:shrink-0">
      {narrow && (
        <Button
          asChild
          variant="ghost"
          size="sm"
          className="-ml-2 w-fit self-start"
        >
          <Link to={SKILLS_PATH}>
            <ArrowLeft aria-hidden="true" />
            {ts('Back to the skills')}
          </Link>
        </Button>
      )}
      <div className="flex min-h-8 flex-wrap items-center gap-x-2 gap-y-1">
        <h2 className="min-w-0 truncate font-mono text-base font-semibold">
          {name}
        </h2>
        <SkillStateBadge skill={state} showValid />
        {dirty && (
          <span
            role="img"
            aria-label={ts('Unsaved changes')}
            title={ts('Unsaved changes')}
            className="size-1.5 shrink-0 rounded-full bg-warning"
          />
        )}
        <div className="ml-auto flex shrink-0 items-center gap-1">
          <IconButton
            label={ts('Download skill')}
            icon={Download}
            busy={downloading}
            onClick={() => void download()}
          />
          {canWrite && (
            <>
              <IconButton
                label={ts('Revert changes')}
                icon={Undo2}
                disabled={!activeDirty || busy}
                onClick={revert}
              />
              <IconButton
                label={ts('Delete skill')}
                icon={Trash2}
                tone="destructive"
                onClick={() => {
                  setDeleteError(null);
                  setConfirming('skill');
                }}
              />
              <Button
                variant="primary"
                size="sm"
                disabled={!canSave}
                onClick={() => void save()}
              >
                <Save aria-hidden="true" />
                {busy && activeDirty ? ts('Saving...') : ts('Save')}
              </Button>
            </>
          )}
        </div>
      </div>
      <SkillMeter content={skillText} />
      {onSkillMd && (
        <div className="space-y-1">
          <Input
            aria-label={ts('What the skill is for')}
            placeholder={ts('What the skill is for')}
            value={description}
            readOnly={!canWrite}
            disabled={!parsed.ok}
            aria-invalid={
              parsed.ok && description.trim() === '' ? true : undefined
            }
            className="h-8 text-sm"
            onChange={(event) =>
              apply(setSkillKey(skillText, 'description', event.target.value))
            }
          />
          {parsed.ok && description.trim() === '' && (
            <p className="text-xs text-destructive">
              {ts('A description is required.')}
            </p>
          )}
        </div>
      )}
      {sync.conflict && (
        <SkillConflictBanner
          hash={sync.conflict.hash}
          draft={drafts[active] ?? ''}
          busy={busy}
          onKeep={() => dispatch({ type: 'rebase' })}
          onReload={() => void reload()}
        />
      )}
      <NoticeLine notices={notices} />
      <SkillIssuePanel
        issues={refused.length > 0 ? sortIssues(refused) : issues}
        heading={
          refused.length > 0 ? ts('The server refused the save') : undefined
        }
        onOpenFile={(file) =>
          files.some((f) => f.path === file) && setActive(file)
        }
      />
      <div className="flex min-h-[30rem] flex-col gap-3 lg:min-h-0 lg:flex-1 lg:flex-row lg:shrink">
        <SkillFileTree
          files={files}
          active={active}
          issues={issues}
          dirtyPaths={dirtyPaths}
          canWrite={canWrite}
          busy={busy}
          onSelect={setActive}
          onAdd={() => setDialog('add')}
          onUpload={() => setDialog('upload')}
          className="max-h-44 shrink-0 rounded-lg border border-border p-1 lg:max-h-none lg:w-56"
        />
        <div className="flex min-h-[18rem] min-w-0 flex-1 flex-col gap-2">
          <div className="flex min-h-7 flex-wrap items-center gap-x-2 gap-y-1">
            <span
              data-testid="skill-active-file"
              className="min-w-0 truncate font-mono text-xs font-medium"
            >
              {active}
            </span>
            {activeFile && (
              <span className="text-[11px] text-muted-foreground">
                {formatBytes(activeFile.size)}
              </span>
            )}
            <div className="ml-auto flex items-center gap-1">
              {textFile && isMarkdown && (
                <div
                  role="group"
                  aria-label={ts('Editor')}
                  className="mr-1 flex overflow-hidden rounded-md border border-border"
                >
                  {(['edit', 'preview'] as const).map((id) => (
                    <button
                      key={id}
                      type="button"
                      aria-pressed={mode === id}
                      onClick={() => setMode(id)}
                      className={cn(
                        'px-2 py-1 text-xs transition-colors',
                        mode === id
                          ? 'bg-accent text-accent-foreground'
                          : 'text-muted-foreground hover:text-foreground'
                      )}
                    >
                      {id === 'edit' ? ts('Edit') : ts('Preview')}
                    </button>
                  ))}
                </div>
              )}
              {onSkillMd && canWrite && parsed.ok && (
                <IconButton
                  size="sm"
                  label={ts('Edit the front matter')}
                  icon={FileCode}
                  pressed={formWhole}
                  disabled={parsed.form.unknown.length > 0}
                  onClick={() => setWholeFile(!wholeFile)}
                />
              )}
              {!onSkillMd && canWrite && (
                <>
                  <IconButton
                    size="sm"
                    label={ts('Rename the file')}
                    icon={Pencil}
                    disabled={busy}
                    onClick={() => setDialog('rename')}
                  />
                  <IconButton
                    size="sm"
                    label={ts('Delete the file')}
                    icon={Trash2}
                    tone="destructive"
                    disabled={busy}
                    onClick={() => {
                      setDeleteError(null);
                      setConfirming('file');
                    }}
                  />
                </>
              )}
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-hidden rounded-lg border border-border">
            {!textFile && activeFile ? (
              <SkillBinaryView
                key={activeFile.path}
                name={name}
                remoteNode={remoteNode}
                file={activeFile}
              />
            ) : readFailure ? (
              <p role="alert" className="p-4 text-sm text-destructive">
                {readFailure}
              </p>
            ) : loadingText ? (
              <PageLoading />
            ) : mode === 'preview' && isMarkdown ? (
              <div className="h-full overflow-y-auto p-4">
                <WikiPageMarkdownPreview content={text} />
              </div>
            ) : (
              <Suspense fallback={<PageLoading />}>
                <MarkdownEditor
                  key={active}
                  value={text}
                  language={language}
                  readOnly={!canWrite}
                  onChange={canWrite ? onText : undefined}
                />
              </Suspense>
            )}
          </div>
        </div>
      </div>
      <SkillUsage name={name} unusable={state.errors.length > 0} />
      {dialog && (
        <SkillFileDialog
          mode={dialog}
          from={active}
          exists={files.map((file) => file.path)}
          onSubmit={submitDialog}
          onClose={() => setDialog(null)}
        />
      )}
      <ConfirmDialog
        title={confirming === 'file' ? ts('Delete file?') : ts('Delete skill?')}
        buttonText={ts('Delete')}
        visible={confirming !== null}
        dismissModal={() => !busy && setConfirming(null)}
        onSubmit={() =>
          void (confirming === 'file'
            ? removeActiveFile()
            : removeSkillFolder())
        }
        submitDisabled={busy}
        initialFocus="cancel"
      >
        <p className="text-sm">
          {confirming === 'file'
            ? ts('Delete "{name}" from the skill "{skill}"?', {
                name: active,
                skill: name,
              })
            : ts(
                'Delete "{name}"? A step that names this skill fails until you create it again.',
                { name }
              )}
        </p>
        {deleteError && (
          <p role="alert" className="mt-2 text-sm text-destructive">
            {deleteError}
          </p>
        )}
      </ConfirmDialog>
      {confirmDialog}
    </div>
  );
}
