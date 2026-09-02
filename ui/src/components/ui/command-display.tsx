import React from 'react';
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip';
import { Code, Terminal, ChevronRight } from 'lucide-react';
import { components } from '../../api/v1/schema';
import { I18nText } from '@/i18n/I18nText';

type CommandEntry = components['schemas']['CommandEntry'];

interface CommandDisplayProps {
  commands?: CommandEntry[];
  icon?: 'code' | 'terminal';
  maxLength?: number;
  className?: string;
  showFullInTooltip?: boolean;
}

const formatCommand = (entry: CommandEntry): string => {
  if (entry.args && entry.args.length > 0) {
    return `${entry.command} ${entry.args.join(' ')}`;
  }
  return entry.command;
};

const truncateText = (
  text: string,
  maxLength: number
): { truncated: string; isTruncated: boolean } => {
  if (text.length <= maxLength) {
    return { truncated: text, isTruncated: false };
  }

  const cutoff = maxLength - 3;
  let breakPoint = cutoff;

  for (let i = cutoff; i > cutoff - 10 && i > 0; i--) {
    if (' /-_=&|'.includes(text[i] || '')) {
      breakPoint = i;
      break;
    }
  }

  return {
    truncated: text.substring(0, breakPoint) + '...',
    isTruncated: true,
  };
};

export const CommandDisplay: React.FC<CommandDisplayProps> = ({
  commands,
  icon = 'code',
  maxLength = 60,
  className = '',
  showFullInTooltip = true,
}) => {
  const Icon = icon === 'code' ? Code : Terminal;

  if (!commands || commands.length === 0) {
    return null;
  }

  // Single command - simple display
  if (commands.length === 1) {
    const entry = commands[0]!;
    const fullCmd = formatCommand(entry);
    const { truncated, isTruncated } = truncateText(fullCmd, maxLength);

    const element = (
      <div
        className={`flex items-center gap-1.5 text-xs font-medium ${className}`}
      >
        <Icon className="h-4 w-4 text-primary flex-shrink-0" />
        <span className="bg-muted rounded-md px-1.5 py-0.5 text-foreground/90 font-mono">
          {truncated}
        </span>
      </div>
    );

    if (!showFullInTooltip || !isTruncated) return element;

    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <div className="cursor-pointer">{element}</div>
        </TooltipTrigger>
        <TooltipContent className="max-w-[600px]">
          <pre className="whitespace-pre-wrap break-all text-xs font-mono">
            {fullCmd}
          </pre>
        </TooltipContent>
      </Tooltip>
    );
  }

  // Multiple commands - structured list
  const element = (
    <div className={`space-y-1 ${className}`}>
      <div className="flex items-center gap-1.5 text-xs">
        <Icon className="h-4 w-4 text-primary flex-shrink-0" />
        <span className="text-muted-foreground font-medium">
          {commands.length} <I18nText text={'commands'} />
        </span>
      </div>
      <div className="pl-3 border-l-2 border-primary/20 space-y-0.5">
        {commands.map((entry, idx) => {
          const fullCmd = formatCommand(entry);
          const { truncated } = truncateText(fullCmd, maxLength - 5);
          return (
            <div
              key={idx}
              className="flex items-center gap-1 text-xs font-mono"
            >
              <ChevronRight className="h-3 w-3 text-muted-foreground flex-shrink-0" />
              <span className="text-foreground/80 truncate">{truncated}</span>
            </div>
          );
        })}
      </div>
    </div>
  );

  if (!showFullInTooltip) return element;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div className="cursor-pointer">{element}</div>
      </TooltipTrigger>
      <TooltipContent className="max-w-[600px]">
        <div className="space-y-2">
          <div className="text-xs font-semibold text-muted-foreground">
            <I18nText
              text="Commands ({count}):"
              values={{ count: commands.length }}
            />
          </div>
          <div className="space-y-1">
            {commands.map((entry, i) => (
              <div key={i} className="flex gap-2 text-xs font-mono">
                <span className="text-muted-foreground w-4 text-right flex-shrink-0">
                  {i + 1}.
                </span>
                <pre className="whitespace-pre-wrap break-all flex-1">
                  {formatCommand(entry)}
                </pre>
              </div>
            ))}
          </div>
        </div>
      </TooltipContent>
    </Tooltip>
  );
};
