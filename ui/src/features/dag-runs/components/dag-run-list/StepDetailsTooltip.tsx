import React from 'react';
import { components, NodeStatus } from '../../../../api/v1/schema';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { AppBarContext } from '../../../../contexts/AppBarContext';
import { isAbortLikeError } from '../../../../lib/requestTimeout';
import {
  fetchDAGRunDetails,
  type DAGRunDetails,
} from '../../hooks/dagRunDetailsRequest';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

type DAGRunSummary = components['schemas']['DAGRunSummary'];
type Node = components['schemas']['Node'];

type StepDetailsTooltipProps = {
  dagRun: DAGRunSummary;
  children: React.ReactNode;
};

const HOVER_FETCH_DELAY_MS = 400;
const HOVER_CACHE_TTL_MS = 30_000;
const hoverDetailsCache = new Map<
  string,
  { expiresAt: number; details: DAGRunDetails }
>();

function toError(
  error: unknown,
  fallbackMessage: string = 'Failed to load step details'
): Error {
  return error instanceof Error ? error : new Error(fallbackMessage);
}

function getStepName(node: Node, index: number) {
  return node.step?.name || `Step ${index + 1}`;
}

function renderStepList(
  title: string,
  nodes: Node[],
  colorClass: string,
  ts: ReturnType<typeof useI18n>['ts']
) {
  if (nodes.length === 0) {
    return null;
  }

  const maxVisible = 5;
  const visibleSteps = nodes.slice(0, maxVisible);
  const remaining = nodes.length - visibleSteps.length;

  return (
    <div className="space-y-1">
      <div className={`text-xs font-semibold ${colorClass}`}>
        <I18nText text={title} />
      </div>
      <ul className="text-xs space-y-0.5">
        {visibleSteps.map((node, idx) => (
          <li key={`${node.step?.name || idx}-${idx}`}>
            {idx + 1}. {getStepName(node, idx)}
          </li>
        ))}
      </ul>
      {remaining > 0 && (
        <div className="text-xs text-muted-foreground">
          {ts(remaining === 1 ? '+{count} more step' : '+{count} more steps', {
            count: remaining,
          })}
        </div>
      )}
    </div>
  );
}

export function StepDetailsTooltip({
  dagRun,
  children,
}: StepDetailsTooltipProps) {
  const { ts } = useI18n();
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const [isOpen, setIsOpen] = React.useState(false);
  const [details, setDetails] = React.useState<DAGRunDetails | null>(null);
  const [error, setError] = React.useState<Error | null>(null);
  const [isLoading, setIsLoading] = React.useState(false);
  const fetchTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(
    null
  );
  const controllerRef = React.useRef<AbortController | null>(null);

  const canRequestDetails = Boolean(dagRun.name && dagRun.dagRunId);
  const cacheKey = `${remoteNode}:${dagRun.name}:${dagRun.dagRunId}`;

  React.useEffect(() => {
    if (fetchTimerRef.current) {
      clearTimeout(fetchTimerRef.current);
      fetchTimerRef.current = null;
    }
    if (controllerRef.current) {
      controllerRef.current.abort();
      controllerRef.current = null;
    }

    if (!isOpen || !canRequestDetails) {
      setIsLoading(false);
      setError(null);
      return;
    }

    const cached = hoverDetailsCache.get(cacheKey);
    if (cached && cached.expiresAt > Date.now()) {
      setDetails(cached.details);
      setError(null);
      setIsLoading(false);
      return;
    }

    setDetails(null);
    setError(null);
    setIsLoading(true);

    fetchTimerRef.current = setTimeout(() => {
      const controller = new AbortController();
      controllerRef.current = controller;

      void fetchDAGRunDetails(
        {
          remoteNode,
          name: dagRun.name,
          dagRunId: dagRun.dagRunId,
        },
        { signal: controller.signal }
      )
        .then((nextDetails) => {
          if (controller.signal.aborted) {
            return;
          }
          hoverDetailsCache.set(cacheKey, {
            details: nextDetails,
            expiresAt: Date.now() + HOVER_CACHE_TTL_MS,
          });
          setDetails(nextDetails);
          setError(null);
        })
        .catch((fetchError) => {
          if (controller.signal.aborted && isAbortLikeError(fetchError)) {
            return;
          }
          setError(toError(fetchError, ts('Failed to load step details')));
        })
        .finally(() => {
          if (controllerRef.current === controller) {
            controllerRef.current = null;
            setIsLoading(false);
          }
        });
    }, HOVER_FETCH_DELAY_MS);

    return () => {
      if (fetchTimerRef.current) {
        clearTimeout(fetchTimerRef.current);
        fetchTimerRef.current = null;
      }
      if (controllerRef.current) {
        controllerRef.current.abort();
        controllerRef.current = null;
      }
    };
  }, [
    cacheKey,
    canRequestDetails,
    dagRun.dagRunId,
    dagRun.name,
    isOpen,
    remoteNode,
  ]);

  const nodes = details?.nodes || [];
  const runningSteps = nodes.filter(
    (node) => node.status === NodeStatus.Running
  );
  const retryingSteps = nodes.filter(
    (node) => node.status === NodeStatus.Retrying
  );
  const failedSteps = nodes.filter((node) => node.status === NodeStatus.Failed);

  const hasStepData =
    runningSteps.length > 0 ||
    retryingSteps.length > 0 ||
    failedSteps.length > 0;

  return (
    <Tooltip open={isOpen} onOpenChange={setIsOpen}>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent className="max-w-sm space-y-2">
        {!canRequestDetails && (
          <div className="text-xs text-muted-foreground">
            <I18nText text={'Step details are unavailable for this DAG run.'} />
          </div>
        )}
        {canRequestDetails && isLoading && (
          <div className="text-xs text-muted-foreground">
            <I18nText text={'Loading step details...'} />
          </div>
        )}
        {canRequestDetails && error && (
          <div className="text-xs text-error">
            {error.message || <I18nText text={'Failed to load step details'} />}
          </div>
        )}
        {canRequestDetails && !isLoading && !error && (
          <>
            {hasStepData ? (
              <>
                {renderStepList(
                  'Running steps',
                  runningSteps,
                  'text-success',
                  ts
                )}
                {renderStepList(
                  'Retrying steps',
                  retryingSteps,
                  'text-warning',
                  ts
                )}
                {renderStepList('Failed steps', failedSteps, 'text-error', ts)}
              </>
            ) : (
              <div className="text-xs text-muted-foreground">
                <I18nText
                  text={'No running, retrying, or failed steps at the moment.'}
                />
              </div>
            )}
          </>
        )}
      </TooltipContent>
    </Tooltip>
  );
}
