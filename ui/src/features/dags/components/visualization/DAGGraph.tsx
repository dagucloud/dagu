/**
 * DAGGraph component provides a tabbed interface for visualizing DAG dagRuns as either a graph or timeline.
 *
 * @module features/dags/components/visualization
 */
import { Tab, Tabs } from '@/components/ui/tabs';
import { GanttChart, GitGraph } from 'lucide-react';
import React from 'react';
import { useCookies } from 'react-cookie';
import { components } from '../../../../api/v1/schema';
import { useConfig } from '../../../../contexts/ConfigContext';
import BorderedBox from '@/components/ui/bordered-box';
import type { SubRunStackEntry } from '../common';
import { FlowchartType, Graph, TimelineChart } from './';
import { GraphInteractionsHint } from './GraphInteractionsHint';
import {
  GraphResizeHandle,
  useResizableGraphHeight,
} from './GraphResizeHandle';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the DAGGraph component
 */
type Props = {
  /** DAG dagRun details containing execution information */
  dagRun: components['schemas']['DAGRunDetails'];
  /** Callback for when a step is inspected in the graph (click) */
  onClickStep?: (id: string) => void;
  /** Callback for when a step is selected in the graph (double-click) */
  onSelectStep?: (id: string) => void;
  /** Callback for when a step is right-clicked in the graph */
  onRightClickStep?: (id: string) => void;
  /** Callback for opening a child DAG-run from the timeline */
  onOpenSubRun?: (entry: SubRunStackEntry) => void;
};

/**
 * DAGGraph component provides a tabbed interface for visualizing DAG dagRuns
 * with options to switch between graph and timeline views
 */
function DAGGraph({
  dagRun,
  onClickStep,
  onSelectStep,
  onRightClickStep,
  onOpenSubRun,
}: Props) {
  // Active tab state (0 = Graph, 1 = Timeline)
  const [sub, setSub] = React.useState('0');
  const config = useConfig();

  // Flowchart direction preference stored in cookies
  const [cookie, setCookie] = useCookies(['flowchart']);
  const [flowchart, setFlowchart] = React.useState(cookie['flowchart']);
  const {
    height: graphHeight,
    graphBoxRef,
    handleProps: graphResizeHandleProps,
  } = useResizableGraphHeight();
  const hasStepInspector = Boolean(onClickStep);

  /**
   * Handle flowchart direction change and save preference to cookie
   */
  const onChangeFlowchart = (value: FlowchartType) => {
    if (!value) {
      return;
    }
    setCookie('flowchart', value, { path: '/' });
    setFlowchart(value);
  };

  return (
    <div>
      <div className="flex flex-col sm:flex-row sm:justify-between sm:items-start mb-4 gap-2">
        <Tabs className="w-auto self-center sm:self-auto">
          <Tab
            isActive={sub === '0'}
            onClick={() => setSub('0')}
            className="flex items-center gap-2 cursor-pointer"
          >
            <GitGraph className="h-4 w-4" />
            <I18nText text={'Graph'} />
          </Tab>
          <Tab
            isActive={sub === '1'}
            onClick={() => setSub('1')}
            className="flex items-center gap-2 cursor-pointer"
          >
            <GanttChart className="h-4 w-4" />
            <I18nText text={'Timeline'} />
          </Tab>
        </Tabs>

        <div className="self-center sm:self-auto"></div>
      </div>

      <BorderedBox className="pt-4 px-4 pb-0 flex flex-col items-stretch overflow-hidden">
        {sub === '0' && (
          <GraphInteractionsHint
            className="mb-2"
            inspect={hasStepInspector}
            openSubRun
            updateStatus={config.permissions.runDags && !!onRightClickStep}
          />
        )}
        <div ref={graphBoxRef} className="overflow-x-auto -mx-4 px-4">
          {sub === '0' ? (
            <Graph
              steps={dagRun.nodes}
              name={dagRun.name}
              type="status"
              flowchart={flowchart}
              onChangeFlowchart={onChangeFlowchart}
              onClickNode={onClickStep ?? onSelectStep}
              selectOnClick={hasStepInspector}
              onDoubleClickNode={hasStepInspector ? onSelectStep : undefined}
              onRightClickNode={
                config.permissions.runDags ? onRightClickStep : undefined
              }
              height={graphHeight}
            />
          ) : (
            <TimelineChart status={dagRun} onOpenSubRun={onOpenSubRun} />
          )}
        </div>
        {sub === '0' && <GraphResizeHandle {...graphResizeHandleProps} />}
        {sub === '1' && <div className="pb-4" />}
      </BorderedBox>
    </div>
  );
}

export default DAGGraph;
