// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { BookOpen, Download, Plus, Search } from 'lucide-react';
import {
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactElement,
} from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  EmptyState,
  LoadErrorState,
  PageLoading,
} from '@/components/PageStates';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanManageSkills } from '@/contexts/AuthContext';
import {
  filterSkills,
  NEW_SKILL_PATH,
  NEW_SKILL_SEGMENT,
  newSkillText,
  SKILLS_PATH,
  skillPath,
} from '@/features/skills/skill';
import { SkillStateBadge } from '@/features/skills/SkillIssueViews';
import { downloadSkillZip, writeSkillText } from '@/features/skills/skillSync';
import { useClient, useQuery } from '@/hooks/api';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { NewSkillDialog } from './NewSkillDialog';
import { SkillEditor } from './SkillEditor';

// The `lg` breakpoint of Tailwind: the list and the editor sit side by side.
const WIDE_QUERY = '(min-width: 1024px)';

/**
 * The skills of the server: instructions that a chat step lets the model
 * load. A wide viewport shows the list and the selected skill side by side. A
 * narrow viewport shows one of them. The address selects the skill.
 */
export function SkillsTab(): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const navigate = useNavigate();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const canWrite = useCanManageSkills();
  const wide = useMediaQuery(WIDE_QUERY, true);
  const { name } = useParams<{ name: string }>();
  const [query, setQuery] = useState('');
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);

  const creating = name === NEW_SKILL_SEGMENT;
  const selected = name && !creating ? name : null;

  const { data, error, mutate } = useQuery(
    '/skills',
    { params: { query: { remoteNode } } },
    { revalidateOnMount: true, revalidateOnFocus: true }
  );
  const skills = data?.skills;

  // A skill that the list no longer has leaves the selection.
  const listed = skills?.map((skill) => skill.name).join('\n');
  useEffect(() => {
    const names = new Set((listed ?? '').split('\n'));
    setPicked((previous) => {
      const next = new Set([...previous].filter((item) => names.has(item)));
      return next.size === previous.size ? previous : next;
    });
  }, [listed]);

  const create = useCallback(
    async (skill: string): Promise<string | null> => {
      const outcome = await writeSkillText(
        client,
        { name: skill, remoteNode },
        newSkillText(skill),
        'create',
        ts('The skill was not saved.')
      );
      if (!outcome.ok) {
        return outcome.kind === 'conflict'
          ? ts('A skill named "{name}" exists.', { name: skill })
          : outcome.message;
      }
      await mutate();
      navigate(skillPath(skill));
      return null;
    },
    [client, remoteNode, ts, mutate, navigate]
  );

  const download = async () => {
    setDownloading(true);
    setDownloadError(null);
    try {
      await downloadSkillZip(client, remoteNode, [...picked]);
    } catch (failure) {
      setDownloadError(
        failure instanceof Error ? failure.message : ts('The download failed.')
      );
    } finally {
      setDownloading(false);
    }
  };

  if (!skills && error) {
    return (
      <LoadErrorState
        title={ts("Couldn't load the skills")}
        error={error}
        onRetry={() => void mutate()}
      />
    );
  }
  if (!skills) return <PageLoading />;

  const shown = filterSkills(skills, query);
  const newButton = canWrite ? (
    <Button asChild variant="primary" size="sm" className="h-8">
      <Link to={NEW_SKILL_PATH}>
        <Plus aria-hidden="true" />
        {ts('New skill')}
      </Link>
    </Button>
  ) : null;

  const list = (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <div className="flex items-center gap-2">
        {skills.length > 0 && (
          <div className="relative min-w-0 flex-1">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              aria-label={ts('Search skills')}
              placeholder={ts('Search skills')}
              className="h-8 pl-8 text-xs"
            />
          </div>
        )}
        {newButton}
      </div>
      {skills.length > 0 && (
        <div className="flex items-center gap-2 px-1">
          <Checkbox
            aria-label={ts('Select all skills')}
            checked={
              shown.length > 0 && shown.every((skill) => picked.has(skill.name))
                ? true
                : shown.some((skill) => picked.has(skill.name))
                  ? 'indeterminate'
                  : false
            }
            disabled={shown.length === 0}
            onCheckedChange={(checked) =>
              setPicked((previous) => {
                const next = new Set(previous);
                for (const skill of shown) {
                  if (checked === true) next.add(skill.name);
                  else next.delete(skill.name);
                }
                return next;
              })
            }
          />
          <span aria-live="polite" className="text-xs text-muted-foreground">
            {picked.size > 0
              ? ts('{count} selected', { count: picked.size })
              : ts('Select all')}
          </span>
          <Button
            variant="outline"
            size="sm"
            className="ml-auto h-7"
            disabled={picked.size === 0 || downloading}
            onClick={() => void download()}
          >
            <Download aria-hidden="true" />
            {ts('Download')}
          </Button>
        </div>
      )}
      {downloadError && (
        <p role="alert" className="px-1 text-xs text-destructive">
          {downloadError}
        </p>
      )}
      {skills.length === 0 ? (
        <p className="text-sm text-muted-foreground">{ts('No skills yet')}</p>
      ) : shown.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">
          {ts('No skill matches "{query}"', { query: query.trim() })}
        </p>
      ) : (
        <ul
          aria-label={ts('Skills')}
          className="min-h-0 flex-1 space-y-1 overflow-y-auto"
        >
          {shown.map((skill) => (
            <li key={skill.name} className="flex items-start gap-2">
              <Checkbox
                aria-label={ts('Select skill {name}', { name: skill.name })}
                className="mt-3 ml-1"
                checked={picked.has(skill.name)}
                onCheckedChange={(checked) =>
                  setPicked((previous) => {
                    const next = new Set(previous);
                    if (checked === true) next.add(skill.name);
                    else next.delete(skill.name);
                    return next;
                  })
                }
              />
              <Link
                to={skillPath(skill.name)}
                aria-current={selected === skill.name ? 'page' : undefined}
                className={cn(
                  'block min-w-0 flex-1 rounded-md border px-3 py-2 transition-colors',
                  selected === skill.name
                    ? 'border-border bg-accent'
                    : 'border-transparent hover:bg-muted'
                )}
              >
                <span className="flex items-center gap-2">
                  <span className="min-w-0 truncate font-mono text-sm font-medium">
                    {skill.name}
                  </span>
                  <SkillStateBadge skill={skill} className="ml-auto" />
                </span>
                <span className="line-clamp-2 break-words text-xs text-muted-foreground">
                  {skill.description}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );

  const detail = selected ? (
    <SkillEditor
      // The draft belongs to one skill on one node.
      key={`${remoteNode}/${selected}`}
      name={selected}
      remoteNode={remoteNode}
      canWrite={canWrite}
      narrow={!wide}
      onChanged={() => void mutate()}
    />
  ) : (
    <EmptyState
      icon={<BookOpen />}
      title={ts('No skill selected')}
      description={ts(
        'A skill is a set of instructions. The model reads it only when it needs it.'
      )}
      action={newButton}
    />
  );

  // The editor keeps one place in the tree at each width: a move to another
  // place would mount it again and drop its draft.
  return (
    <>
      <div
        className={
          wide
            ? 'grid h-full min-h-0 grid-cols-[20rem_minmax(0,1fr)] gap-4 xl:grid-cols-[24rem_minmax(0,1fr)]'
            : 'h-full min-h-0'
        }
      >
        {(wide || selected === null) && (
          <div className={wide ? 'min-h-0' : 'h-full min-h-0'}>{list}</div>
        )}
        {(wide || selected !== null) && (
          <div
            className={
              wide
                ? 'min-h-0 rounded-lg border border-border bg-card p-4'
                : 'h-full min-h-0'
            }
          >
            {detail}
          </div>
        )}
      </div>
      {creating && (
        <NewSkillDialog
          existing={skills.map((skill) => skill.name)}
          onCreate={create}
          onClose={() => navigate(SKILLS_PATH)}
        />
      )}
    </>
  );
}
