import { TableCell } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { statusClasses } from '@/lib/statusPalette';
import { components, NodeStatus } from '../../../../api/v1/schema';
import StyledTableRow from '@/components/ui/styled-table-row';

/**
 * Props for the HistoryTableRow component
 */
type Props = {
  /** Grid data for the row */
  data: components['schemas']['DAGGridItem'];
  /** Callback for when a cell is selected */
  onSelect: (idx: number) => void;
  /** Currently selected index */
  idx: number;
};

/**
 * The cells hold the status of a step in each run, so the values are
 * NodeStatus, not Status. The dot takes its colour from the status palette,
 * as the canvas and the dock do: a skipped step is grey there too.
 */
function getStatusStyling(status: NodeStatus) {
  return {
    dotClass: statusClasses(status).dot,
    pulseAnimation: status === NodeStatus.Running ? 'animate-pulse' : '',
  };
}

/**
 * HistoryTableRow displays a row in the execution history table
 * with colored circles representing the status of each run
 */
function HistoryTableRow({ data, onSelect, idx }: Props) {
  return (
    <StyledTableRow className="hover:bg-muted transition-colors duration-200">
      <TableCell className="whitespace-nowrap font-medium text-sm">
        {data.name}
      </TableCell>
      {[...data.history].reverse().map((status, i) => {
        // Determine if this cell should be highlighted
        const isSelected = i === idx;
        const { dotClass, pulseAnimation } = getStatusStyling(status);

        return (
          <TableCell
            key={i}
            onClick={() => onSelect(i)}
            className={cn(
              'max-w-[22px] min-w-[22px] p-2 text-center cursor-pointer',
              'hover:bg-muted/50 transition-colors duration-200',
              isSelected && 'bg-accent'
            )}
          >
            {status !== NodeStatus.NotStarted && (
              <div
                className={cn(
                  'w-[12px] h-[12px] rounded-full transition-[scale,background-color] duration-300 mx-auto hover:scale-110',
                  dotClass,
                  pulseAnimation
                )}
              />
            )}
          </TableCell>
        );
      })}
    </StyledTableRow>
  );
}

export default HistoryTableRow;
