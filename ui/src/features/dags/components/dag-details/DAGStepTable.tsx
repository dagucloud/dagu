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
          <TableHead className="w-[4%] text-center">No</TableHead>
          <TableHead className="w-[28%]">Step Details</TableHead>
          <TableHead className="w-[22%]">Execution</TableHead>
          <TableHead className="w-[14%]">Dependencies</TableHead>
          <TableHead className="w-[18%]">Configuration</TableHead>
          <TableHead className="w-[14%]">Conditions</TableHead>
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
