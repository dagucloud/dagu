import { Button } from '@/components/ui/button';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { Clock, Maximize, RotateCcw, ZoomIn, ZoomOut } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
import { DataSet } from 'vis-data';
import { Timeline } from 'vis-timeline/standalone';
import 'vis-timeline/styles/vis-timeline-graph2d.css';
import { components } from '../../../api/v1/schema';
import { statusColorMapping } from '../../../consts';
import { useConfig } from '../../../contexts/ConfigContext';
import dayjs from '../../../lib/dayjs';
import DAGRunDetailsModal from '../../dag-runs/components/dag-run-details/DAGRunDetailsModal';
import { I18nText } from '@/i18n/I18nText';

type Props = {
  data: components['schemas']['DAGRunSummary'][];
  selectedDate?: {
    startTimestamp: number;
    endTimestamp?: number;
  };
};

type TimelineItem = {
  id: string;
  content: string;
  start: Date;
  end: Date;
  group: string;
  className: string;
};

function DashboardTimeChart({ data: input, selectedDate }: Props) {
  const timelineRef = useRef<HTMLDivElement>(null);
  const timelineInstance = useRef<Timeline | null>(null);
  const timelineItemsRef = useRef(new DataSet<TimelineItem>([]));
  const initialViewRef = useRef<{ start: Date; end: Date } | null>(null);
  const appliedSelectedDateRef = useRef<{
    startTimestamp: number | null;
    endTimestamp: number | null;
  } | null>(null);
  const config = useConfig();
  const [selectedDAGRun, setSelectedDAGRun] = useState<{
    name: string;
    dagRunId: string;
  } | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);

  // Helper function to ensure we have a valid IANA timezone
  const getValidTimezone = React.useCallback((tz: string): string => {
    try {
      dayjs().tz(tz);
      return tz;
    } catch {
      if (tz.startsWith('UTC+') || tz.startsWith('UTC-')) {
        return (
          'Etc/GMT' + (tz.startsWith('UTC+') ? '-' : '+') + tz.substring(4)
        );
      }
      return dayjs.tz.guess();
    }
  }, []);

  // Function to determine appropriate time scale based on visible range
  const updateTimeAxisBasedOnZoom = React.useCallback((timeline: Timeline) => {
    try {
      const range = timeline.getWindow();
      const rangeInMs = range.end.getTime() - range.start.getTime();
      const rangeInMinutes = rangeInMs / (1000 * 60);
      const rangeInHours = rangeInMinutes / 60;
      const rangeInDays = rangeInHours / 24;

      let options = {};

      if (rangeInMinutes < 30) {
        // Less than 30 minutes - show 1-minute intervals
        options = {
          timeAxis: { scale: 'minute', step: 1 },
          format: {
            minorLabels: {
              second: 's',
              minute: 'HH:mm:ss',
            },
            majorLabels: {
              minute: 'HH:mm',
              hour: 'ddd D MMM HH:mm',
            },
          },
        };
      } else if (rangeInMinutes < 120) {
        // 30 minutes to 2 hours - show 5-minute intervals
        options = {
          timeAxis: { scale: 'minute', step: 5 },
          format: {
            minorLabels: {
              minute: 'HH:mm',
              hour: 'HH:mm',
            },
            majorLabels: {
              hour: 'ddd D MMM',
              day: 'ddd D MMM',
            },
          },
        };
      } else if (rangeInHours < 6) {
        // 2-6 hours - show 15-minute intervals
        options = {
          timeAxis: { scale: 'minute', step: 15 },
          format: {
            minorLabels: {
              minute: 'HH:mm',
              hour: 'HH:mm',
            },
            majorLabels: {
              hour: 'ddd D MMM',
              day: 'ddd D MMM',
            },
          },
        };
      } else if (rangeInHours < 24) {
        // 6-24 hours - show hourly
        options = {
          timeAxis: { scale: 'hour', step: 1 },
          format: {
            minorLabels: {
              hour: 'HH:mm',
            },
            majorLabels: {
              day: 'ddd D MMM',
            },
          },
        };
      } else if (rangeInDays < 3) {
        // 1-3 days - show 2-hour intervals
        options = {
          timeAxis: { scale: 'hour', step: 2 },
          format: {
            minorLabels: {
              hour: 'HH:mm',
              day: 'D',
            },
            majorLabels: {
              day: 'ddd D MMM',
              week: 'MMM YYYY',
            },
          },
        };
      } else if (rangeInDays < 7) {
        // 3-7 days - show 4-hour intervals
        options = {
          timeAxis: { scale: 'hour', step: 4 },
          format: {
            minorLabels: {
              hour: 'HH:mm',
              day: 'D',
            },
            majorLabels: {
              day: 'ddd D MMM',
              week: 'MMM YYYY',
            },
          },
        };
      } else if (rangeInDays < 30) {
        // 7-30 days - show daily
        options = {
          timeAxis: { scale: 'day', step: 1 },
          format: {
            minorLabels: {
              day: 'D',
              weekday: 'ddd',
            },
            majorLabels: {
              week: 'W',
              month: 'MMM YYYY',
            },
          },
        };
      } else if (rangeInDays < 90) {
        // 30-90 days - show 2-day intervals
        options = {
          timeAxis: { scale: 'day', step: 2 },
          format: {
            minorLabels: {
              day: 'D',
              week: 'W',
            },
            majorLabels: {
              month: 'MMM YYYY',
            },
          },
        };
      } else if (rangeInDays < 365) {
        // 90-365 days - show weekly
        options = {
          timeAxis: { scale: 'week', step: 1 },
          format: {
            minorLabels: {
              week: 'W',
              month: 'MMM',
            },
            majorLabels: {
              month: 'MMM YYYY',
              year: 'YYYY',
            },
          },
        };
      } else {
        // More than 365 days - show monthly
        options = {
          timeAxis: { scale: 'month', step: 1 },
          format: {
            minorLabels: {
              month: 'MMM',
            },
            majorLabels: {
              year: 'YYYY',
            },
          },
        };
      }

      timeline.setOptions(options);
    } catch (error) {
      console.warn('Error updating time axis:', error);
    }
  }, []);

  // Store input data in a ref for click handler access
  const inputRef = useRef(input);
  useEffect(() => {
    inputRef.current = input;
  }, [input]);

  // Initialize timeline once on mount
  useEffect(() => {
    if (!timelineRef.current || timelineInstance.current) return;

    const now = dayjs();
    const viewStartDate = selectedDate
      ? dayjs.unix(selectedDate.startTimestamp)
      : dayjs().startOf('day');
    const viewEndDate = selectedDate?.endTimestamp
      ? dayjs.unix(selectedDate.endTimestamp)
      : now.endOf('day');

    const validViewStartDate = !isNaN(viewStartDate.toDate().getTime())
      ? viewStartDate.toDate()
      : dayjs().startOf('day').toDate();
    const validViewEndDate = !isNaN(viewEndDate.toDate().getTime())
      ? viewEndDate.toDate()
      : dayjs().endOf('day').toDate();

    initialViewRef.current = {
      start: validViewStartDate,
      end: validViewEndDate,
    };

    timelineInstance.current = new Timeline(
      timelineRef.current,
      timelineItemsRef.current,
      {
        start: validViewStartDate,
        end: validViewEndDate,
        orientation: 'top',
        stack: true,
        showMajorLabels: true,
        showMinorLabels: true,
        showTooltips: true,
        zoomable: true,
        verticalScroll: true,
        zoomKey: 'ctrlKey',
        timeAxis: { scale: 'hour', step: 1 },
        format: {
          minorLabels: {
            minute: 'HH:mm',
            hour: 'HH:mm',
          },
          majorLabels: {
            hour: 'ddd D MMM',
            day: 'ddd D MMM',
          },
        },
        height: '100%',
        maxHeight: '100%',
        margin: {
          item: { vertical: 4, horizontal: 2 },
          axis: 2,
        },
      }
    );

    // Add range change listener for dynamic time axis
    timelineInstance.current.on('rangechanged', () => {
      if (timelineInstance.current) {
        updateTimeAxisBasedOnZoom(timelineInstance.current);
      }
    });

    // Register click handler
    timelineInstance.current.on('click', (properties) => {
      if (properties.item) {
        const itemId = properties.item.toString();
        const matchingDAGRun = inputRef.current.find(
          (dagRun) => itemId === dagRun.name + `_${dagRun.dagRunId}`
        );
        if (matchingDAGRun) {
          setSelectedDAGRun({
            name: matchingDAGRun.name,
            dagRunId: matchingDAGRun.dagRunId,
          });
          setIsModalOpen(true);
        }
      }
    });

    // Initial update based on current view
    updateTimeAxisBasedOnZoom(timelineInstance.current);

    return () => {
      if (timelineInstance.current) {
        timelineInstance.current.off('rangechanged');
        timelineInstance.current.off('click');
        timelineInstance.current.destroy();
        timelineInstance.current = null;
      }
    };
  }, []); // Only run once on mount

  // Update timeline data when input changes (without recreating timeline)
  useEffect(() => {
    if (!timelineInstance.current) return;

    const validTimezone = getValidTimezone(config.tz);
    const nextItems = new Map<string, TimelineItem>();
    const now = dayjs();

    input.forEach((dagRun) => {
      const status = dagRun.status;
      const start = dagRun.startedAt;
      if (start && start !== '-') {
        const startMoment = dayjs(start);
        const end =
          dagRun.finishedAt && dagRun.finishedAt !== '-'
            ? dayjs(dagRun.finishedAt)
            : now;

        const startDate = startMoment.tz(validTimezone).toDate();
        const endDate = end.tz(validTimezone).toDate();

        const id = dagRun.name + `_${dagRun.dagRunId}`;
        if (nextItems.has(id)) return; // Skip duplicates

        if (
          !isNaN(startDate.getTime()) &&
          !isNaN(endDate.getTime()) &&
          startDate <= endDate
        ) {
          nextItems.set(id, {
            id,
            content: dagRun.name,
            start: startDate,
            end: endDate,
            group: 'main',
            className: `status-${status}`,
          });
        }
      }
    });

    const dataset = timelineItemsRef.current;
    const existingItems = new Map(
      dataset.get().map((item) => [item.id, item] as const)
    );
    const updates: TimelineItem[] = [];
    const removals: string[] = [];

    nextItems.forEach((nextItem, id) => {
      const currentItem = existingItems.get(id);
      if (
        !currentItem ||
        currentItem.content !== nextItem.content ||
        currentItem.group !== nextItem.group ||
        currentItem.className !== nextItem.className ||
        currentItem.start.getTime() !== nextItem.start.getTime() ||
        currentItem.end.getTime() !== nextItem.end.getTime()
      ) {
        updates.push(nextItem);
      }
      existingItems.delete(id);
    });

    existingItems.forEach((_item, id) => {
      removals.push(id);
    });

    if (removals.length > 0) {
      dataset.remove(removals);
    }
    if (updates.length > 0) {
      dataset.update(updates);
    }
  }, [input, config.tz, getValidTimezone]);

  // Update the window only when the selected date values change.
  useEffect(() => {
    if (!timelineInstance.current) return;

    const nextSelectedDate = {
      startTimestamp: selectedDate?.startTimestamp ?? null,
      endTimestamp: selectedDate?.endTimestamp ?? null,
    };
    const previousSelectedDate = appliedSelectedDateRef.current;
    if (
      previousSelectedDate &&
      previousSelectedDate.startTimestamp === nextSelectedDate.startTimestamp &&
      previousSelectedDate.endTimestamp === nextSelectedDate.endTimestamp
    ) {
      return;
    }

    const now = dayjs();
    const viewStartDate = selectedDate
      ? dayjs.unix(selectedDate.startTimestamp)
      : dayjs().startOf('day');
    const viewEndDate = selectedDate?.endTimestamp
      ? dayjs.unix(selectedDate.endTimestamp)
      : now.endOf('day');

    const validViewStartDate = !isNaN(viewStartDate.toDate().getTime())
      ? viewStartDate.toDate()
      : dayjs().startOf('day').toDate();
    const validViewEndDate = !isNaN(viewEndDate.toDate().getTime())
      ? viewEndDate.toDate()
      : dayjs().endOf('day').toDate();

    timelineInstance.current.setWindow(validViewStartDate, validViewEndDate);

    // Update initial view ref for reset button
    initialViewRef.current = {
      start: validViewStartDate,
      end: validViewEndDate,
    };
    appliedSelectedDateRef.current = nextSelectedDate;
  }, [selectedDate?.endTimestamp, selectedDate?.startTimestamp]);

  const handleCloseModal = () => {
    setIsModalOpen(false);
  };

  const handleZoomIn = () => {
    if (timelineInstance.current) {
      timelineInstance.current.zoomIn(0.5);
    }
  };

  const handleZoomOut = () => {
    if (timelineInstance.current) {
      timelineInstance.current.zoomOut(0.5);
    }
  };

  const handleFit = () => {
    if (timelineInstance.current) {
      try {
        timelineInstance.current.fit();
      } catch (error) {
        console.warn('Timeline fit failed:', error);
      }
    }
  };

  const handleCurrent = () => {
    if (timelineInstance.current) {
      const now = dayjs();
      timelineInstance.current.setWindow(
        now.subtract(1, 'hour').toDate(),
        now.add(1, 'hour').toDate()
      );
    }
  };

  const handleReset = () => {
    if (timelineInstance.current && initialViewRef.current) {
      timelineInstance.current.setWindow(
        initialViewRef.current.start,
        initialViewRef.current.end
      );
    }
  };

  return (
    <div className="w-full h-full flex flex-col bg-card overflow-hidden">
      {/* Toolbar */}
      <div className="flex justify-between items-center gap-2 px-3 py-1.5 bg-muted/30 flex-shrink-0">
        <span className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          <I18nText text={"Timeline"} />
        </span>
        <div className="flex items-center gap-0.5 rounded-md border border-border bg-card p-0.5">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                onClick={handleCurrent}
                className="h-7 w-7 rounded-sm"
              >
                <Clock className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">
              <p><I18nText text={"Current time"} /></p>
            </TooltipContent>
          </Tooltip>

          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                onClick={handleFit}
                className="h-7 w-7 rounded-sm"
              >
                <Maximize className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">
              <p><I18nText text={"Fit all"} /></p>
            </TooltipContent>
          </Tooltip>

          <div className="w-px h-4 bg-border mx-0.5" />

          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                onClick={handleZoomIn}
                className="h-7 w-7 rounded-sm"
              >
                <ZoomIn className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">
              <p><I18nText text={"Zoom in"} /></p>
            </TooltipContent>
          </Tooltip>

          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                onClick={handleZoomOut}
                className="h-7 w-7 rounded-sm"
              >
                <ZoomOut className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">
              <p><I18nText text={"Zoom out"} /></p>
            </TooltipContent>
          </Tooltip>

          <div className="w-px h-4 bg-border mx-0.5" />

          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                onClick={handleReset}
                className="h-7 w-7 rounded-sm"
              >
                <RotateCcw className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">
              <p><I18nText text={"Reset view"} /></p>
            </TooltipContent>
          </Tooltip>
        </div>
      </div>

      {/* Timeline content */}
      <div ref={timelineRef} className="flex-1 min-h-0 overflow-auto" />

      {/* DAG Run details modal */}
      {selectedDAGRun && (
        <DAGRunDetailsModal
          name={selectedDAGRun.name}
          dagRunId={selectedDAGRun.dagRunId}
          isOpen={isModalOpen}
          onClose={handleCloseModal}
        />
      )}
      <style>
        {`
        .vis-timeline {
          font-family: var(--font-sans) !important;
          font-size: 11px !important;
          background-color: var(--background) !important;
          border: 1px solid var(--border) !important;
          border-radius: 12px !important;
          overflow: hidden !important;
        }
        .vis-timeline .vis-panel {
          border-color: var(--border) !important;
        }
        .vis-timeline .vis-panel.vis-left {
          display: none !important;
        }
        .vis-item .vis-item-overflow {
          overflow: visible !important;
          color: var(--foreground) !important;
        }
        .vis-panel.vis-top {
          position: sticky;
          top: 0;
          z-index: 1;
          background-color: var(--card) !important;
          border-bottom: 1px solid var(--border) !important;
        }
        .vis-labelset {
          position: sticky;
          left: 0;
          z-index: 2;
          background-color: var(--background) !important;
          border-right: 1px solid var(--border) !important;
        }
        .vis-labelset .vis-label {
          color: var(--muted-foreground) !important;
          padding: 4px 8px !important;
          border-bottom: 1px solid var(--border) !important;
        }
        .vis-foreground {
          background-color: transparent !important;
        }
        .vis-background {
          background-color: var(--background) !important;
        }
        .vis-center {
          background-color: var(--background) !important;
        }
        .vis-time-axis {
          background-color: var(--card) !important;
          color: var(--foreground) !important;
        }
        .vis-time-axis .vis-text {
          font-size: 10px !important;
          color: var(--muted-foreground) !important;
          font-family: var(--font-sans) !important;
        }
        .vis-time-axis .vis-text.vis-major {
          color: var(--foreground) !important;
          font-weight: 600;
        }
        .vis-time-axis .vis-grid.vis-minor {
          border-color: var(--border) !important;
          opacity: 0.5;
        }
        .vis-time-axis .vis-grid.vis-major {
          border-color: var(--border) !important;
        }
        .vis-item .vis-item-content {
          position: absolute !important;
          left: 100% !important;
          padding-left: 8px !important;
          transform: translateY(-50%) !important;
          top: 50% !important;
          white-space: nowrap !important;
          font-size: 10px !important;
          font-weight: 400 !important;
          color: var(--foreground) !important;
          writing-mode: horizontal-tb !important;
          text-orientation: mixed !important;
        }
        .vis-item {
          overflow: visible !important;
          height: 14px !important;
          border-radius: 4px !important;
          border-width: 0 !important;
          cursor: pointer !important;
          transition: none !important;
          animation: none !important;
          box-shadow: 0 0 10px rgba(0,0,0,0.3);
        }
        .vis-item,
        .vis-item * {
          transition: none !important;
          animation: none !important;
        }
        .vis-item:hover {
          filter: brightness(1.2);
          transform: scaleY(1.2);
          z-index: 100 !important;
        }
        .vis-item.vis-selected {
          border-width: 1px !important;
          border-color: var(--primary) !important;
          box-shadow: 0 0 15px rgba(var(--primary-rgb), 0.3);
        }
        .vis-current-time {
          background-color: var(--primary) !important;
          width: 2px !important;
          box-shadow: 0 0 10px rgba(var(--primary-rgb), 0.4);
        }
        /* Scrollbar styling for timeline */
        .vis-timeline::-webkit-scrollbar {
          width: 8px;
          height: 8px;
        }
        .vis-timeline::-webkit-scrollbar-track {
          background: var(--background);
        }
        .vis-timeline::-webkit-scrollbar-thumb {
          background: var(--muted);
          border-radius: 10px;
          border: 2px solid var(--background);
        }
        .vis-timeline::-webkit-scrollbar-thumb:hover {
          background: var(--secondary);
        }
        `}
      </style>
      <style>{`
        ${Object.entries(statusColorMapping)
          .map(
            ([status, color]) => `
          .status-${status.toLowerCase()} {
            background-color: ${color.backgroundColor};
            color: ${color.color};
            border-color: ${color.backgroundColor};
          }
        `
          )
          .join('\n')}
      `}</style>
    </div>
  );
}

export default DashboardTimeChart;
