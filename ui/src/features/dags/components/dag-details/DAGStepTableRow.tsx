/**
 * DAGStepTableRow component renders a single row in the DAG step table.
 *
 * @module features/dags/components/dag-details
 */
import { CommandDisplay } from '@/components/ui/command-display';
import { ScriptBadge } from '@/components/ui/script-dialog';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { isHarnessStep } from '@/lib/harness-step';
import { getExecutorCommand, getLogStepMessage } from '@/lib/executor-utils';
import {
  ArrowRight,
  Code,
  Folder,
  GitBranch,
  GitFork,
  Mail,
  RefreshCw,
} from 'lucide-react';
import { components } from '../../../../api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { TableCell, TableRow } from '@/components/ui/table';
import HarnessStepSummary from './HarnessStepSummary';
import { LogStepMessage } from './LogStepMessage';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the DAGStepTableRow component
 */
type Props = {
  /** Step definition to display */
  step: components['schemas']['Step'];
  /** Index of the step in the list */
  index: number;
};

/**
 * DAGStepTableRow displays information about a single step in a DAG
 */
function DAGStepTableRow({ step, index }: Props) {
  const subDagName = step.call;
  const logMessage = getLogStepMessage(step);
  // Format preconditions as a list
  const preconditions = step.preconditions?.map((c, index) => (
    <div
      key={index}
      className="flex items-center gap-1.5 mb-1 text-xs bg-muted rounded-md p-1.5"
    >
      <span className="font-medium text-foreground/90">{c.condition}</span>
      <span className="text-muted-foreground">=&gt;</span>
      <span className="text-foreground/90">{c.expected}</span>
    </div>
  ));

  return (
    <TableRow className="h-auto transition-colors duration-200 hover:bg-muted/50 [&_td]:align-top">
      {/* Number */}
      <TableCell className="text-center font-semibold text-foreground/90 text-xs py-2">
        {index + 1}
      </TableCell>

      {/* Step Details */}
      <TableCell>
        <div className="space-y-1">
          <div className="break-words text-sm font-semibold text-foreground">
            {step.name}
          </div>
          {step.id && (
            <div
              className="truncate font-mono text-xs text-muted-foreground"
              title={step.id}
            >
              <I18nText text={"ID:"} /> {step.id}
            </div>
          )}
          {step.description && (
            <div
              className="line-clamp-2 text-xs leading-relaxed text-muted-foreground"
              title={step.description}
            >
              {step.description}
            </div>
          )}
          {subDagName && (
            <div className="mt-1 flex w-fit items-center gap-1.5 rounded-md bg-info-muted px-1.5 py-0.5 text-xs">
              <GitBranch className="h-3.5 w-3.5 text-info" />
              <span className="font-medium text-info">
                <I18nText text={"Sub-DAG:"} /> {subDagName}
              </span>
            </div>
          )}
          {/* Router Configuration */}
          {step.router && (
            <div className="mt-1 space-y-1">
              <div className="flex items-center gap-1.5 text-xs">
                <GitFork className="h-3.5 w-3.5 text-info" />
                <span className="font-medium"><I18nText text={"Router:"} /></span>
                <code className="bg-muted px-1 rounded">
                  {step.router.value}
                </code>
              </div>
              {step.router.routes && (
                <div className="text-xs bg-muted/50 rounded p-1.5 space-y-0.5">
                  {step.router.routes.map((route, idx) => (
                    <div
                      key={idx}
                      className="flex items-center gap-1 font-mono"
                    >
                      <span className="text-muted-foreground">
                        {route.pattern}
                      </span>
                      <span className="text-muted-foreground">→</span>
                      <span>{route.targets?.join(', ')}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </TableCell>

      {/* Execution */}
      <TableCell>
        <div className="space-y-1.5">
          {isHarnessStep(step) ? (
            <HarnessStepSummary step={step} compact />
          ) : logMessage !== null ? (
            <LogStepMessage message={logMessage} compact />
          ) : step.commands && step.commands.length > 0 ? (
            <CommandDisplay
              commands={step.commands}
              icon="terminal"
              maxLength={50}
            />
          ) : (
            (() => {
              const execCmd = getExecutorCommand(step);
              if (execCmd) {
                return (
                  <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
                    <Code className="h-3.5 w-3.5 text-primary flex-shrink-0" />
                    <span
                      className="font-mono text-xs truncate max-w-[200px]"
                      title={execCmd}
                    >
                      {execCmd.length > 50
                        ? execCmd.slice(0, 47) + '...'
                        : execCmd}
                    </span>
                  </div>
                );
              }
              return null;
            })()
          )}

          {/* Script */}
          {step.script && (
            <ScriptBadge script={step.script} stepName={step.name} />
          )}

          {/* Directory */}
          {step.dir && (
            <div className="flex w-fit max-w-full items-center gap-1.5 rounded-md bg-muted px-1.5 py-0.5 text-xs">
              <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span
                className="truncate font-medium text-muted-foreground"
                title={step.dir}
              >
                {step.dir}
              </span>
            </div>
          )}

          {/* Output */}
          {step.output && (
            <div className="flex items-center gap-1.5 text-xs bg-success-muted rounded-md px-1.5 py-0.5 w-fit">
              <ArrowRight className="h-3.5 w-3.5 text-success" />
              <span className="font-medium text-success">
                <I18nText text={"Output:"} /> {step.output}
              </span>
            </div>
          )}
        </div>
      </TableCell>

      {/* Dependencies */}
      <TableCell>
        {step.depends && step.depends.length > 0 ? (
          <div className="flex flex-wrap gap-1">
            {step.depends.map((dep, idx) => (
              <Badge
                key={idx}
                variant="outline"
                className="bg-muted text-foreground/90 px-1.5 py-0.5 text-wrap break-all text-xs"
              >
                {dep}
              </Badge>
            ))}
          </div>
        ) : (
          <span className="text-xs text-muted-foreground leading-tight">
            <I18nText text={"None"} />
          </span>
        )}
      </TableCell>

      {/* Configuration */}
      <TableCell>
        <div className="min-w-0 space-y-1.5">
          {/* Repeat Policy */}
          {step.repeatPolicy?.repeat && (
            <div className="space-y-1">
              <Badge
                variant="outline"
                className={`flex items-center gap-1.5 px-2 py-0.5 text-xs ${
                  step.repeatPolicy.repeat === 'while'
                    ? 'bg-info-muted text-info border-info/30'
                    : step.repeatPolicy.repeat === 'until'
                      ? 'bg-info-muted text-info border-info/30'
                      : 'bg-primary/10 text-primary border-primary/30'
                }`}
              >
                <RefreshCw className="h-3 w-3" />
                <span className="font-medium uppercase tracking-wider text-xs">
                  {step.repeatPolicy.repeat === 'while'
                    ? 'WHILE'
                    : step.repeatPolicy.repeat === 'until'
                      ? 'UNTIL'
                      : 'REPEAT'}
                </span>
                {step.repeatPolicy.interval && (
                  <span className="text-xs opacity-75">
                    {step.repeatPolicy.interval}s
                  </span>
                )}
                {step.repeatPolicy.limit && (
                  <span className="text-xs opacity-75">
                    ×{step.repeatPolicy.limit}
                  </span>
                )}
              </Badge>

              {/* Repeat Condition */}
              {step.repeatPolicy.condition && (
                <div className="text-xs bg-muted rounded px-1.5 py-0.5 font-mono">
                  <span className="text-muted-foreground">
                    {step.repeatPolicy.repeat === 'while'
                      ? <I18nText text={"↻ while"} />
                      : <I18nText text={"↻ until"} />}
                    :
                  </span>{' '}
                  <span className="text-foreground/90">
                    {step.repeatPolicy.condition.condition}
                  </span>
                  {step.repeatPolicy.condition.expected && (
                    <>
                      <span className="text-muted-foreground"> = </span>
                      <span className="text-success">
                        {step.repeatPolicy.condition.expected}
                      </span>
                    </>
                  )}
                </div>
              )}

              {/* Exit Codes */}
              {step.repeatPolicy.exitCode &&
                step.repeatPolicy.exitCode.length > 0 && (
                  <div className="text-xs bg-muted rounded px-1.5 py-0.5">
                    <span className="text-muted-foreground"><I18nText text={"exit codes:"} /></span>{' '}
                    <span className="font-mono text-warning">
                      [{step.repeatPolicy.exitCode.join(', ')}]
                    </span>
                  </div>
                )}
            </div>
          )}

          {/* Mail on Error */}
          {step.mailOnError && (
            <div className="flex items-center gap-1.5 text-xs bg-error-muted rounded-md px-1.5 py-0.5 w-fit">
              <Mail className="h-3.5 w-3.5 text-error" />
              <span className="font-medium text-error"><I18nText text={"Mail on Error"} /></span>
            </div>
          )}

          {/* Stdout/Stderr */}
          {(step.stdout || step.stderr) && (
            <div className="text-xs text-muted-foreground bg-muted rounded-md p-1.5 leading-tight">
              {step.stdout && (
                <div className="mb-1">
                  <I18nText text={"stdout:"} /> <span className="font-mono">{step.stdout}</span>
                </div>
              )}
              {step.stderr && (
                <div>
                  <I18nText text={"stderr:"} /> <span className="font-mono">{step.stderr}</span>
                </div>
              )}
            </div>
          )}

          {/* Params for Sub-DAG */}
          {step.params && (
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type="button"
                  className="block w-full min-w-0 cursor-pointer rounded-md bg-muted p-1.5 text-left text-xs leading-tight text-muted-foreground"
                >
                  <span className="mb-0.5 block font-medium"><I18nText text={"Params"} /></span>
                  <span className="block truncate font-mono">
                    {step.params}
                  </span>
                </button>
              </TooltipTrigger>
              <TooltipContent className="max-w-[400px]">
                <span className="break-all font-mono text-xs">
                  {step.params}
                </span>
              </TooltipContent>
            </Tooltip>
          )}
        </div>
      </TableCell>

      {/* Preconditions */}
      <TableCell>
        {preconditions && preconditions.length > 0 ? (
          <div className="space-y-1">{preconditions}</div>
        ) : (
          <span className="text-xs text-muted-foreground leading-tight">
            <I18nText text={"None"} />
          </span>
        )}
      </TableCell>
    </TableRow>
  );
}

export default DAGStepTableRow;
