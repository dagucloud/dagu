import { useEffect, useMemo, useRef } from 'react';
import type { ReactElement } from 'react';

import { cn } from '@/lib/utils';

import { SessionWithState } from '../types';
import { formatDate } from '../utils/formatDate';

type Props = {
  isOpen: boolean;
  isMobile?: boolean;
  sessions: SessionWithState[];
  activeSessionId: string | null;
  onSelectSession: (id: string) => void;
  onClose: () => void;
  onLoadMore: () => void;
  hasMore: boolean;
};

export function SessionSidebar({
  isOpen,
  isMobile,
  sessions,
  activeSessionId,
  onSelectSession,
  onClose,
  onLoadMore,
  hasMore,
}: Props): ReactElement | null {
  const sentinelRef = useRef<HTMLDivElement>(null);
  const sortedSessions = useMemo(
    () =>
      [...sessions].sort(
        (a, b) =>
          new Date(b.session.updated_at).getTime() -
          new Date(a.session.updated_at).getTime()
      ),
    [sessions]
  );

  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasMore) return;
    const observer = new IntersectionObserver(
      ([entry]) => { if (entry?.isIntersecting) onLoadMore(); },
      { threshold: 0.1 }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasMore, onLoadMore]);

  if (!isOpen) return null;

  const handleSelect = (id: string) => {
    onSelectSession(id);
    if (isMobile) onClose();
  };

  const list = (
    <div className="flex flex-col h-full bg-card dark:bg-zinc-950 border-r border-border overflow-y-auto">
        {sortedSessions.map((sess) => (
          <button
            key={sess.session.id}
            onClick={() => handleSelect(sess.session.id)}
            className={cn(
              'w-full text-left px-3 py-1.5 text-xs flex items-center gap-1.5 hover:bg-accent/50 transition-colors',
              sess.session.id === activeSessionId && 'bg-accent'
            )}
          >
            {sess.has_pending_prompt ? (
              <span className="h-2 w-2 rounded-full bg-orange-400 flex-shrink-0" role="img" aria-label="Waiting for input" />
            ) : sess.working ? (
              <span className="h-2 w-2 rounded-full bg-green-500 flex-shrink-0" role="img" aria-label="Running" />
            ) : (
              <span className="h-2 w-2 flex-shrink-0" />
            )}
            <span className="truncate">{formatDate(sess.session.updated_at)}</span>
          </button>
        ))}
        {hasMore && <div ref={sentinelRef} className="h-6 flex-shrink-0" />}
    </div>
  );

  if (isMobile) {
    return (
      <>
        <div
          className="absolute inset-0 z-10 bg-black/30"
          onClick={onClose}
        />
        <div className="absolute left-0 top-0 bottom-0 z-20 w-[240px]">
          {list}
        </div>
      </>
    );
  }

  return (
    <div className="w-[208px] shrink-0">
      {list}
    </div>
  );
}
