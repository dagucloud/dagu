/**
 * DAGErrors component displays a list of errors for DAGs.
 *
 * @module features/dags/components/dag-editor
 */
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { AlertCircle } from 'lucide-react';
import { components } from '../../../../api/v1/schema';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the DAGErrors component
 */
type Props = {
  /** List of DAG files */
  dags: components['schemas']['DAGFile'][];
  /** List of general errors */
  errors: string[];
  /** Whether there are any errors */
  hasError: boolean;
};

/**
 * DAGErrors displays a list of errors for DAGs
 * with links to the corresponding DAG pages
 */
function DAGErrors({ dags, errors, hasError }: Props) {
  if (!dags || !hasError) {
    return null;
  }

  return (
    <Alert 
      variant="destructive" 
      className="py-2 mb-2 bg-error-muted border-error/30 text-error"
    >
      <AlertCircle className="h-4 w-4 text-error" />
      <AlertTitle className="text-sm font-medium text-error"><I18nText text={"Error"} /></AlertTitle>
      <AlertDescription className="text-xs mt-1 text-error">
        <ul className="list-disc pl-4 space-y-0.5">
          {dags
            .filter((dag) => dag.errors && dag.errors.length > 0)
            .map((dag) => {
              const url = `/dags/${dag.fileName}`;
              return dag.errors.map((err, index) => (
                <li key={`${dag.dag.name}-${index}`} className="text-xs">
                  <a href={url} className="font-medium underline hover:text-error">
                    {dag.dag.name}
                  </a>
                  : {err}
                </li>
              ));
            })}
          {errors.map((e, index) => (
            <li key={`general-${index}`} className="text-xs">
              {e}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  );
}

export default DAGErrors;
