/**
 * DAGSpecErrors component displays a list of spec errors.
 *
 * @module features/dags/components/dag-editor
 */

/**
 * Props for the DAGSpecErrors component
 */

import { I18nText } from '@/i18n/I18nText';

type Props = {
  /** List of error messages */
  errors: string[];
};

/**
 * DAGSpecErrors displays a list of errors related to the DAG spec
 */
function DAGSpecErrors({ errors }: Props) {
  if (!errors || errors.length == 0) {
    return null;
  }
  return (
    <div className="notification is-danger mt-0 mb-0">
      <div><I18nText text={"Please check the below errors!"} /></div>
      <div className="content">
        <ul>
          {errors.map((e, i) => (
            <li key={`${i}`}>{e}</li>
          ))}
        </ul>
      </div>
    </div>
  );
}

export default DAGSpecErrors;
