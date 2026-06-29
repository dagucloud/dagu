// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { useClient } from '@/hooks/api';
import { cn } from '@/lib/utils';
import React from 'react';
import { Link } from 'react-router-dom';
import { components } from '../../../api/v1/schema';
import { AppBarContext } from '../../../contexts/AppBarContext';

type SearchMatch = components['schemas']['SearchMatchItem'];
type DagResult = components['schemas']['DAGSearchPageItem'];
type DAGWorkspaceQuery = {
  workspace?: string;
};

type LoadMoreResponse = {
  error?: string;
  matches: SearchMatch[];
  hasMore: boolean;
  nextCursor?: string;
};

type Props = {
  query: string;
  results: DagResult[];
  workspaceQuery?: DAGWorkspaceQuery;
};

type SearchResultItemProps = {
  kind: 'DAG';
  title: string;
  link: string;
  query: string;
  initialMatches: SearchMatch[];
  initialHasMoreMatches: boolean;
  initialNextCursor?: string;
  loadMore: (cursor?: string) => Promise<LoadMoreResponse>;
};

function SearchSnippet({ match }: { match: SearchMatch }) {
  const lines = match.line.split('\n');

  return (
    <pre className="overflow-x-auto rounded-md border bg-muted/25 p-3 text-xs leading-5">
      {lines.map((line, index) => {
        const lineNumber = match.startLine + index;
        const isHit = lineNumber === match.lineNumber;

        return (
          <span
            key={`${match.startLine}-${lineNumber}-${index}`}
            className={cn(
              'grid min-w-full w-max grid-cols-[max-content_minmax(0,max-content)] gap-3 px-1',
              isHit && 'rounded bg-primary/10'
            )}
          >
            <span className="w-8 select-none text-right text-[11px] tabular-nums text-muted-foreground">
              {lineNumber}
            </span>
            <code className="whitespace-pre font-mono text-foreground">
              {line || ' '}
            </code>
          </span>
        );
      })}
    </pre>
  );
}

function SearchResultItem({
  kind,
  title,
  link,
  query,
  initialMatches,
  initialHasMoreMatches,
  initialNextCursor,
  loadMore,
}: SearchResultItemProps) {
  const [matches, setMatches] = React.useState<SearchMatch[]>(initialMatches);
  const [hasMoreMatches, setHasMoreMatches] = React.useState(
    initialHasMoreMatches
  );
  const [nextCursor, setNextCursor] = React.useState(initialNextCursor);
  const [isLoadingMore, setIsLoadingMore] = React.useState(false);
  const [loadError, setLoadError] = React.useState<string | null>(null);

  React.useEffect(() => {
    setMatches(initialMatches);
    setHasMoreMatches(initialHasMoreMatches);
    setNextCursor(initialNextCursor);
    setIsLoadingMore(false);
    setLoadError(null);
  }, [initialHasMoreMatches, initialMatches, initialNextCursor, query]);

  const loadMoreMatches = React.useCallback(async () => {
    if (isLoadingMore || !hasMoreMatches) {
      return;
    }

    setIsLoadingMore(true);
    setLoadError(null);

    try {
      const response = await loadMore(nextCursor);
      if (response.error) {
        setLoadError(response.error);
        return;
      }

      setMatches((current) => [...current, ...response.matches]);
      setHasMoreMatches(response.hasMore);
      setNextCursor(response.nextCursor);
    } catch {
      setLoadError('Failed to load more matches');
    } finally {
      setIsLoadingMore(false);
    }
  }, [hasMoreMatches, isLoadingMore, loadMore, nextCursor]);

  return (
    <li className="px-4 py-4">
      <div className="flex flex-col gap-3">
        <div className="flex items-start justify-between gap-4">
          <Link to={link} className="block min-w-0">
            <h3 className="text-lg font-semibold text-foreground whitespace-normal break-words">
              {title}
              <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-xs font-normal text-muted-foreground">
                {kind}
              </span>
            </h3>
          </Link>
          <span className="shrink-0 text-xs text-muted-foreground">
            {matches.length} shown
          </span>
        </div>

        {matches.map((match, index) => (
          <SearchSnippet
            key={`${link}-${match.lineNumber}-${match.startLine}-${index}`}
            match={match}
          />
        ))}

        {loadError && (
          <div className="text-sm text-destructive">{loadError}</div>
        )}

        {hasMoreMatches && (
          <div>
            <Button
              variant="outline"
              size="sm"
              disabled={isLoadingMore}
              onClick={() => {
                void loadMoreMatches();
              }}
            >
              {isLoadingMore ? 'Loading...' : 'Show more matches'}
            </Button>
          </div>
        )}
      </div>
    </li>
  );
}

function SearchResult(props: Props) {
  const { query, results } = props;
  const client = useClient();
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const dagWorkspaceQuery = props.workspaceQuery ?? {};

  const items = results.map((result) => {
    const linkSearch = result.workspace
      ? `?workspace=${encodeURIComponent(result.workspace)}`
      : '';
    return {
      key: `dag-${result.fileName}-${result.workspace ?? ''}-${query}`,
      kind: 'DAG' as const,
      title: result.name,
      link: `/dags/${encodeURI(result.fileName)}/spec${linkSearch}`,
      initialMatches: result.matches ?? [],
      initialHasMoreMatches: result.hasMoreMatches,
      initialNextCursor: result.nextMatchesCursor,
      loadMore: async (cursor?: string): Promise<LoadMoreResponse> => {
        try {
          const response = await client.GET('/search/dags/{fileName}/matches', {
            params: {
              path: { fileName: result.fileName },
              query: {
                remoteNode,
                q: query,
                cursor,
                ...dagWorkspaceQuery,
              },
            },
          });

          return {
            error: response.error?.message || undefined,
            matches: response.data?.matches ?? [],
            hasMore: response.data?.hasMore ?? false,
            nextCursor: response.data?.nextCursor,
          };
        } catch (error) {
          return {
            error:
              error instanceof Error
                ? error.message
                : 'Failed to load more matches',
            matches: [],
            hasMore: false,
          };
        }
      },
    };
  });

  return (
    <ul className="divide-y rounded-md border">
      {items.map((item) => (
        <SearchResultItem
          key={item.key}
          kind={item.kind}
          title={item.title}
          link={item.link}
          query={query}
          initialMatches={item.initialMatches}
          initialHasMoreMatches={item.initialHasMoreMatches}
          initialNextCursor={item.initialNextCursor}
          loadMore={item.loadMore}
        />
      ))}
    </ul>
  );
}

export default SearchResult;
