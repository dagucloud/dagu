import { Bot } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  getHarnessStepSummary,
  type HarnessAttemptSummary,
  type HarnessStepSummary as HarnessSummary,
} from '@/lib/harness-step';
import { cn } from '@/lib/utils';
import { components } from '../../../../api/v1/schema';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type Props = {
  step: components['schemas']['Step'];
  className?: string;
  compact?: boolean;
};

const visibleOptionCount = 3;
const promptTooltipThreshold = 120;

function HarnessStepSummary({ step, className, compact = false }: Props) {
  const summary = getHarnessStepSummary(step);

  if (!summary) {
    return null;
  }

  if (compact) {
    return <CompactHarnessSummary summary={summary} className={className} />;
  }

  const promptContent = summary.prompt ? (
    <div className="rounded-md border border-border/70 bg-background/70 px-2 py-1.5">
      <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        <I18nText text={'Prompt'} />
      </div>
      <div className="line-clamp-3 whitespace-pre-wrap break-words text-xs leading-relaxed text-foreground/90">
        {summary.prompt}
      </div>
    </div>
  ) : null;

  return (
    <div
      className={cn(
        'rounded-lg border border-info/20 bg-info/5 p-2 space-y-2',
        className
      )}
    >
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge
          variant="info"
          className="h-6 gap-1.5 px-2.5 normal-case tracking-normal"
        >
          <Bot className="h-3.5 w-3.5" />
          <I18nText text={'Harness'} />
        </Badge>
      </div>

      {summary.attempts.map((attempt, index) => (
        <HarnessAttemptRow
          key={`${attempt.label}-${attempt.provider || index}`}
          attempt={attempt}
          isPrimary={index === 0}
        />
      ))}

      {summary.prompt && summary.prompt.length > promptTooltipThreshold ? (
        <Tooltip>
          <TooltipTrigger asChild>
            <div className="cursor-help">{promptContent}</div>
          </TooltipTrigger>
          <TooltipContent className="max-w-[640px]">
            <div className="whitespace-pre-wrap break-words text-xs leading-relaxed">
              {summary.prompt}
            </div>
          </TooltipContent>
        </Tooltip>
      ) : (
        promptContent
      )}
    </div>
  );
}

function CompactHarnessSummary({
  summary,
  className,
}: {
  summary: HarnessSummary;
  className?: string;
}) {
  const primary = summary.attempts[0];
  const fallbackCount = Math.max(summary.attempts.length - 1, 0);
  const optionCount = summary.attempts.reduce(
    (count, attempt) => count + attempt.options.length,
    0
  );

  return (
    <div className={cn('min-w-0 space-y-1.5', className)}>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <Badge variant="info" className="normal-case tracking-normal">
          <Bot className="h-3 w-3" />
          <I18nText text={'Harness'} />
        </Badge>
        {primary?.provider ? (
          <I18nProps>
            <Badge
              variant="secondary"
              className="font-mono normal-case tracking-normal"
              title="Primary provider"
            >
              {primary.provider}
            </Badge>
          </I18nProps>
        ) : null}
        {optionCount > 0 ? (
          <Badge variant="outline" className="normal-case tracking-normal">
            <I18nText
              text={optionCount === 1 ? '{count} option' : '{count} options'}
              values={{ count: optionCount }}
            />
          </Badge>
        ) : null}
        {fallbackCount > 0 ? (
          <Badge variant="outline" className="normal-case tracking-normal">
            <I18nText
              text={
                fallbackCount === 1 ? '{count} fallback' : '{count} fallbacks'
              }
              values={{ count: fallbackCount }}
            />
          </Badge>
        ) : null}
      </div>
      {summary.prompt ? (
        <div
          className="truncate text-xs text-muted-foreground"
          title={summary.prompt}
        >
          {summary.prompt}
        </div>
      ) : null}
    </div>
  );
}

function HarnessAttemptRow({
  attempt,
  isPrimary,
}: {
  attempt: HarnessAttemptSummary;
  isPrimary: boolean;
}) {
  const visibleOptions = attempt.options.slice(0, visibleOptionCount);
  const hiddenOptions = attempt.options.slice(visibleOptionCount);

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Badge
        variant={isPrimary ? 'primary' : 'outline'}
        className="h-5 px-2 normal-case tracking-normal"
      >
        {attempt.label}
      </Badge>
      {attempt.provider && (
        <Badge
          variant={isPrimary ? 'info' : 'secondary'}
          className="h-5 px-2 font-mono normal-case tracking-normal"
        >
          {attempt.provider}
        </Badge>
      )}
      {visibleOptions.map((option) => (
        <Badge
          key={`${attempt.label}-${option}`}
          variant="outline"
          className="h-5 px-2 font-mono normal-case tracking-normal"
        >
          {option}
        </Badge>
      ))}
      {hiddenOptions.length > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge
              variant="outline"
              className="h-5 cursor-help px-2 normal-case tracking-normal"
            >
              <I18nText
                text="+{count} more"
                values={{ count: hiddenOptions.length }}
              />
            </Badge>
          </TooltipTrigger>
          <TooltipContent className="max-w-[480px]">
            <div className="flex flex-wrap gap-1.5">
              {hiddenOptions.map((option) => (
                <Badge
                  key={`${attempt.label}-hidden-${option}`}
                  variant="outline"
                  className="h-5 px-2 font-mono normal-case tracking-normal"
                >
                  {option}
                </Badge>
              ))}
            </div>
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  );
}

export default HarnessStepSummary;
