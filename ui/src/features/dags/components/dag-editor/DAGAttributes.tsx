/**
 * DAGAttributes component displays the attributes of a DAG.
 *
 * @module features/dags/components/dag-editor
 */
import dayjs from '@/lib/dayjs';
import { Calendar, CheckSquare, Settings, Tag } from 'lucide-react';
import { components } from '../../../../api/v1/schema';
import { Badge } from '@/components/ui/badge';
import {
  getScheduleKey,
  getScheduleLabel,
  parseNextRun,
} from '../../../../lib/dagSchedule';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the DAGAttributes component
 */
type Props = {
  /** DAG details to display */
  dag: components['schemas']['DAGDetails'];
};

/**
 * DAGAttributes displays the metadata and configuration of a DAG
 * including name, schedule, description, and other properties
 */
function DAGAttributes({ dag }: Props) {
  const { locale } = useI18n();
  const nextRun = parseNextRun(dag.nextRun);

  return (
    <div>
      <h2 className="text-xl font-semibold text-foreground mb-4">{dag.name}</h2>

      {dag.description && (
        <p className="text-muted-foreground mb-6">{dag.description}</p>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {/* Schedule */}
        <div className="space-y-1 md:col-span-2">
          <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
            <Calendar className="h-4 w-4" />
            <span><I18nText text={"Schedule"} /></span>
          </div>

          {!dag.schedule?.length ? (
            <div className="text-sm text-muted-foreground italic">
              <I18nText text={"No schedule defined"} />
            </div>
          ) : (
            <div className="space-y-2">
              <div className="flex flex-wrap gap-2">
                {dag.schedule?.map((schedule, index) => (
                  <Badge
                    key={getScheduleKey(schedule, index)}
                    variant="outline"
                    title={
                      schedule.kind === 'at'
                        ? schedule.at || undefined
                        : schedule.expression || undefined
                    }
                    className="max-w-full justify-start bg-primary/10 px-2.5 py-1 text-primary border-primary/30 whitespace-nowrap normal-case tracking-normal"
                  >
                    {getScheduleLabel(schedule)}
                  </Badge>
                ))}
              </div>

              <div className="text-sm text-muted-foreground">
                <span className="font-medium text-foreground"><I18nText text={"Next run:"} /></span>{' '}
                {nextRun
                  ? `${dayjs(nextRun).format('YYYY-MM-DD HH:mm:ss')} (${dayjs(nextRun).locale(locale === 'zh-CN' ? 'zh-cn' : locale).fromNow()})`
                  : <I18nText text="No upcoming run" />}
              </div>
            </div>
          )}
        </div>

        {/* Parameters */}
        {dag.params && dag.params.length > 0 && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Tag className="h-4 w-4" />
              <span><I18nText text={"Parameters"} /></span>
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

        {/* Max Active Runs */}
        {dag.maxActiveSteps && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Settings className="h-4 w-4" />
              <span><I18nText text={"Max Active Runs"} /></span>
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
              <span><I18nText text={"Preconditions"} /></span>
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
