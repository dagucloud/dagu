/**
 * NodeStatusTable component displays a table of node execution statuses.
 *
 * @module features/dags/components/dag-details
 */
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useConfig } from '@/contexts/ConfigContext';
import { components, NodeStatus } from '../../../../api/v1/schema';
import NodeStatusTableRow from './NodeStatusTableRow';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the NodeStatusTable component
 */
type Props = {
  /** List of nodes to display */
  nodes?: components['schemas']['Node'][];
  /** DAG dagRun details */
  status: components['schemas']['DAGRunDetails'];
  /** DAG file name */
  fileName: string;
  /** Function to open log viewer */
  onViewLog?: (
    stepName: string,
    dagRunId: string,
    node?: components['schemas']['Node']
  ) => void;
  /** Function called after a row status update succeeds */
  onNodeStatusUpdated?: (stepName: string, status: NodeStatus) => void;
  /** Hide per-row actions for nodes the step APIs cannot address */
  hideActions?: boolean;
};

/**
 * NodeStatusTable displays execution status information for all nodes in a DAG run
 */
function NodeStatusTable({
  nodes,
  status,
  fileName,
  onViewLog,
  onNodeStatusUpdated,
  hideActions,
}: Props) {
  const config = useConfig();
  const showActionsColumn = Boolean(
    !hideActions && status?.dagRunId && config.permissions.runDags
  );

  // Don't render if there are no nodes
  if (!nodes || !nodes.length) {
    return null;
  }

  // Surface the first failed step's log without an extra click
  const firstFailedStepName = nodes.find(
    (n) => n.status === NodeStatus.Failed && (n.stdout || n.stderr)
  )?.step.name;

  return (
    <div>
      {/* Desktop view - Table with horizontal scroll for intermediate sizes */}
      <div className="hidden md:block w-full overflow-x-auto">
        <div className="min-w-[900px]">
          <Table className="w-full">
            <TableHeader>
              <TableRow>
                <TableHead className="w-[5%] text-center"><I18nText text={"No"} /></TableHead>
                <TableHead className="w-[20%]"><I18nText text={"Step Name"} /></TableHead>
                <TableHead className="w-[15%]"><I18nText text={"Execution"} /></TableHead>
                <TableHead className="w-[15%]"><I18nText text={"Last Run"} /></TableHead>
                <TableHead className="w-[10%] text-center"><I18nText text={"Status"} /></TableHead>
                <TableHead className="w-[35%] min-w-[150px]">
                  <I18nText text={"Error / Logs"} />
                </TableHead>
                {showActionsColumn && (
                  <TableHead className="w-[8%] text-center"><I18nText text={"Actions"} /></TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {nodes.map((n, idx) => (
                <NodeStatusTableRow
                  key={n.step.name}
                  rownum={idx + 1}
                  node={n}
                  name={fileName}
                  onViewLog={onViewLog}
                  onNodeStatusUpdated={onNodeStatusUpdated}
                  dagRun={status}
                  view="desktop"
                  hideActions={hideActions}
                  defaultLogExpanded={n.step.name === firstFailedStepName}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Mobile view - Cards */}
      <div className="md:hidden space-y-4">
        {nodes.map((n, idx) => (
          <NodeStatusTableRow
            key={n.step.name}
            rownum={idx + 1}
            node={n}
            name={fileName}
            onViewLog={onViewLog}
            onNodeStatusUpdated={onNodeStatusUpdated}
            dagRun={status}
            view="mobile"
            hideActions={hideActions}
            defaultLogExpanded={n.step.name === firstFailedStepName}
          />
        ))}
      </div>
    </div>
  );
}

export default NodeStatusTable;
