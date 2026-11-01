/**
 * DAGAttributes component displays the attributes of a DAG.
 *
 * @module features/dags/components/dag-editor
 */
import { CheckSquare, Settings, Tag, Variable } from 'lucide-react';
import { components } from '../../../../api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { useI18n } from '@/i18n/I18nProvider';
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
 * DAGAttributes displays the configuration of a DAG, such as its parameters
 * and run limits. The schedule is a launcher of the canvas.
 */
function DAGAttributes({ dag }: Props) {
  const { ts } = useI18n();
  const labels = visibleLabels(dag);
  const envNames = envVariableNames(dag.env);

  return (
    <div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
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
