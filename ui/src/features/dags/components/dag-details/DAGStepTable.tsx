/**
 * DAGStepTable component displays a table of steps in a DAG.
 *
 * @module features/dags/components/dag-details
 */
import { components } from '../../../../api/v1/schema';
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import DAGStepTableRow from './DAGStepTableRow';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the DAGStepTable component
 */
type Props = {
  /** List of steps to display */
  steps: components['schemas']['Step'][];
  /** Optional title for the table */
  title?: string;
};

/**
 * DAGStepTable displays a table of steps in a DAG with their properties
 */
function DAGStepTable({ steps }: Props) {
  // Don't render if there are no steps
  if (!steps.length) {
    return null;
  }

  return (
    <Table className="min-w-[960px] table-fixed">
      <TableHeader>
        <TableRow className="h-8">
          <TableHead className="w-[4%] text-center"><I18nText text={"No"} /></TableHead>
          <TableHead className="w-[28%]"><I18nText text={"Step Details"} /></TableHead>
          <TableHead className="w-[22%]"><I18nText text={"Execution"} /></TableHead>
          <TableHead className="w-[14%]"><I18nText text={"Dependencies"} /></TableHead>
          <TableHead className="w-[18%]"><I18nText text={"Configuration"} /></TableHead>
          <TableHead className="w-[14%]"><I18nText text={"Conditions"} /></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {steps.map((step, idx) => (
          <DAGStepTableRow key={idx} step={step} index={idx} />
        ))}
      </TableBody>
    </Table>
  );
}

export default DAGStepTable;
