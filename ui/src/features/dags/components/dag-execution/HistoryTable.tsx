/**
 * HistoryTable component displays a table of execution history for a DAG.
 *
 * @module features/dags/components/dag-execution
 */
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { components } from '../../../../api/v1/schema';
import dayjs from '../../../../lib/dayjs';
import HistoryTableRow from './HistoryTableRow';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the HistoryTable component
 */
type Props = {
  /** List of DAG dagRuns */
  dagRuns: components['schemas']['DAGRunDetails'][];
  /** Grid data for visualization */
  gridData: components['schemas']['DAGGridItem'][];
  /** Callback for when a dagRun is selected */
  onSelect: (idx: number) => void;
  /** Currently selected index */
  idx: number;
};

/**
 * HistoryTable displays a grid of execution history for a DAG
 * with dates as column headers and steps as rows
 */
function HistoryTable({ dagRuns, gridData, onSelect, idx }: Props) {
  return (
    <div className="overflow-hidden">
      <Table className="select-none">
        <TableHeader className="bg-muted">
          <TableRow>
            {/* Sized to the longest step name: the run columns stay narrow. */}
            <TableHead className="w-px whitespace-nowrap py-3">
              <I18nText text={'Step Name'} />
            </TableHead>
            {dagRuns.map((_, i) => {
              if (!dagRuns || i >= dagRuns.length || !dagRuns[i]) {
                return null;
              }

              // Format the date and the time for the column header
              let date;
              let time = '--:--';
              let full;
              const startedAt = dagRuns[i].startedAt;
              if (startedAt && startedAt != '-') {
                date = dayjs(startedAt).format('M/D');
                time = dayjs(startedAt).format('HH:mm');
                full = dayjs(startedAt).format('YYYY-MM-DD HH:mm:ss');
              } else {
                date = dayjs().format('M/D');
              }

              // Only show the date if it's different from the previous column
              let flag = false;
              if (i == 0) {
                flag = true;
              }
              if (i > 0 && dagRuns[i - 1]) {
                flag = dayjs(dagRuns[i - 1]!.startedAt).format('M/D') != date;
              }

              return (
                <TableHead
                  key={`date-${i}`}
                  className={cn(
                    'max-w-[22px] min-w-[22px] text-left p-2 cursor-pointer',
                    'hover:bg-muted/50 transition-colors duration-200',
                    i === idx && 'bg-muted/50'
                  )}
                  title={full}
                  onClick={() => {
                    onSelect(i);
                  }}
                >
                  {/* The day shows once, at its first column. Every column
                      shows its start time, turned to fit 22px. */}
                  <span className={flag ? 'block' : 'sr-only'}>{date}</span>
                  <span
                    data-testid="history-column-time"
                    className="block text-[10px] font-normal tabular-nums text-muted-foreground [writing-mode:vertical-rl]"
                  >
                    {time}
                  </span>
                </TableHead>
              );
            })}
          </TableRow>
        </TableHeader>
        <TableBody>
          {gridData.map((data) => {
            return (
              <HistoryTableRow
                key={data.name}
                data={data}
                onSelect={onSelect}
                idx={idx}
              />
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}

export default HistoryTable;
