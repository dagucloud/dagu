/**
 * DAGAttributes component displays the attributes of a DAG.
 *
 * @module features/dags/components/dag-editor
 */
import { Calendar, CheckSquare, Settings, Tag, Variable } from 'lucide-react';
import { components } from '../../../../api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { RelativeTime } from '@/components/ui/relative-time';
import { useI18n } from '@/i18n/I18nProvider';
import {
  formatFireTime,
  getRestartValue,
  getScheduleKey,
  getScheduleLabel,
  isServiceDAG,
  parseNextRun,
} from '@/lib/dagSchedule';
import { envVariableNames, visibleLabels } from './workflowLabels';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the DAGAttributes component
 */
type Props = {
  /** DAG details to display */
  dag: components['schemas']['DAGDetails'];
};

/**
 * The next run in the zone of its schedule, as the server writes it, with
 * its offset, and how soon it comes.
 */
function NextRun({ time, locale }: { time: string; locale: string }) {
  const lang = locale === 'zh-CN' ? 'zh-cn' : locale;
  const shown = formatFireTime(time, 'YYYY-MM-DD HH:mm:ss Z', lang);
  return (
    <>
      {shown} (<RelativeTime timestamp={time} absolute={shown} />)
    </>
  );
}

/**
 * DAGAttributes displays the configuration of a DAG, such as its schedule
 * with its next run, its parameters and its run limits. The canvas also
 * draws the schedule as a launcher, but not for a graph without a start
 * step, such as an agent DAG.
 */
function DAGAttributes({ dag }: Props) {
  const { ts, locale } = useI18n();
  const labels = visibleLabels(dag);
  const envNames = envVariableNames(dag.env);
  const nextRun = parseNextRun(dag.nextRun);

  return (
    <div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {/* Schedule */}
        {!!dag.schedule?.length && (
          <div className="space-y-1 md:col-span-2">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Calendar className="h-4 w-4" />
              <span>{ts('Schedule')}</span>
            </div>
            <div className="flex flex-wrap gap-2">
              {dag.schedule.map((schedule, index) => (
                <Badge
                  key={getScheduleKey(schedule, index)}
                  variant="outline"
                  title={
                    schedule.kind === 'at'
                      ? schedule.at || undefined
                      : schedule.expression || undefined
                  }
                  className="h-auto max-w-full justify-start whitespace-normal break-words bg-primary/10 px-2.5 py-1 text-primary border-primary/30 normal-case tracking-normal"
                >
                  {getScheduleLabel(schedule)}
                </Badge>
              ))}
            </div>
            <div className="text-sm text-muted-foreground">
              <span className="font-medium text-foreground">
                {ts('Next run:')}
              </span>{' '}
              {nextRun && dag.nextRun ? (
                <NextRun time={dag.nextRun} locale={locale} />
              ) : (
                ts('No upcoming run')
              )}
            </div>
          </div>
        )}

        {/* Restart policy, in the place of a schedule */}
        {isServiceDAG(dag) && (
          <div className="space-y-1 md:col-span-2">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Calendar className="h-4 w-4" />
              <span>{ts('Schedule')}</span>
            </div>
            <Badge
              variant="outline"
              className="h-auto max-w-full justify-start whitespace-normal break-words bg-primary/10 px-2.5 py-1 text-primary border-primary/30 normal-case tracking-normal"
            >
              <I18nText text={'Restart'} />: {getRestartValue(dag)}
            </Badge>
          </div>
        )}

        {/* Parameters */}
        {dag.params && dag.params.length > 0 && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Tag className="h-4 w-4" />
              <span>
                <I18nText text={'Parameters'} />
              </span>
            </div>

            <div className="flex flex-wrap gap-2">
              {dag.params.map((param) => (
                <Badge
                  key={param}
                  variant="outline"
                  className="bg-muted text-foreground/90 px-2.5 py-1"
                >
                  {param}
                </Badge>
              ))}
            </div>
          </div>
        )}

        {/* Labels */}
        {labels.length > 0 && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Tag className="h-4 w-4" />
              <span>
                <I18nText text={'Labels'} />
              </span>
            </div>

            <div className="flex flex-wrap gap-2">
              {labels.map((label) => (
                <Badge
                  key={label}
                  variant="outline"
                  className="h-auto max-w-full whitespace-normal wrap-anywhere bg-muted text-foreground/90 px-2.5 py-1"
                >
                  {label}
                </Badge>
              ))}
            </div>
          </div>
        )}

        {/* Environment variables: names only, a value may be a secret */}
        {envNames.length > 0 && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Variable className="h-4 w-4" />
              <span>
                <I18nText text={'Environment variables'} />
              </span>
            </div>

            <div className="flex flex-wrap gap-2">
              {envNames.map((name) => (
                <Badge
                  key={name}
                  variant="outline"
                  className="h-auto max-w-full whitespace-normal wrap-anywhere bg-muted font-mono text-foreground/90 px-2.5 py-1 normal-case tracking-normal"
                >
                  {name}
                </Badge>
              ))}
            </div>
          </div>
        )}

        {/* Max Active Steps: maxActiveRuns is ignored for local queues */}
        {dag.maxActiveSteps && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Settings className="h-4 w-4" />
              <span>{ts('Max Active Steps')}</span>
            </div>

            <div className="font-medium text-foreground">
              {dag.maxActiveSteps}
            </div>
          </div>
        )}

        {/* Preconditions */}
        {dag.preconditions && dag.preconditions.length > 0 && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <CheckSquare className="h-4 w-4" />
              <span>
                <I18nText text={'Preconditions'} />
              </span>
            </div>

            <div className="space-y-2">
              {dag.preconditions.map((c, index) => (
                <div
                  key={index}
                  className="flex items-center gap-2 text-xs bg-muted rounded-md p-2"
                >
                  <span className="font-medium text-foreground/90">
                    {c.condition}
                  </span>
                  <span className="text-muted-foreground">=&gt;</span>
                  <span className="text-foreground/90">{c.expected}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export default DAGAttributes;
