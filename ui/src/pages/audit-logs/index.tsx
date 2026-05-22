// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { DateRangePicker } from '@/components/ui/date-range-picker';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { AppBarContext } from '@/contexts/AppBarContext';
import { TOKEN_KEY, useCanViewAuditLogs } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';
import { ChevronLeft, ChevronRight, RefreshCw, ScrollText } from 'lucide-react';
import { useCallback, useContext, useEffect, useRef, useState } from 'react';

type AuditEntry = components['schemas']['AuditEntry'];

const CATEGORIES = [
  { value: 'all', label: 'All Categories' },
  { value: 'terminal', label: 'Terminal' },
  { value: 'user', label: 'User' },
  { value: 'dag', label: 'DAG' },
  { value: 'api_key', label: 'API Key' },
  { value: 'webhook', label: 'Webhook' },
  { value: 'notification', label: 'Notification' },
  { value: 'git_sync', label: 'Git Sync' },
  { value: 'agent', label: 'Agent' },
  { value: 'mcp', label: 'MCP' },
  { value: 'secret', label: 'Secret' },
  { value: 'workspace', label: 'Workspace' },
  { value: 'system', label: 'System' },
];

const SOURCES = [
  { value: 'all', label: 'All Sources' },
  { value: 'rest', label: 'REST' },
  { value: 'mcp', label: 'MCP' },
];

const SURFACES = [
  { value: 'all', label: 'All Surfaces' },
  { value: 'rest_api', label: 'REST API' },
  { value: 'mcp', label: 'MCP' },
];

const RESULTS = [
  { value: 'all', label: 'All Results' },
  { value: 'succeeded', label: 'Succeeded' },
  { value: 'failed', label: 'Failed' },
  { value: 'denied', label: 'Denied' },
  { value: 'started', label: 'Started' },
  { value: 'received', label: 'Received' },
];

const PAGE_SIZE = 50;

export default function AuditLogsPage() {
  const config = useConfig();
  const canViewAuditLogs = useCanViewAuditLogs();
  const appBarContext = useContext(AppBarContext);
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Filter states
  const [category, setCategory] = useState('all');
  const [source, setSource] = useState('all');
  const [surface, setSurface] = useState('all');
  const [result, setResult] = useState('all');
  const [action, setAction] = useState('');
  const [workspace, setWorkspace] = useState('');
  const [credentialId, setCredentialId] = useState('');
  const [correlationId, setCorrelationId] = useState('');
  const [resourceId, setResourceId] = useState('');
  const [mcpTool, setMcpTool] = useState('');
  const [offset, setOffset] = useState(0);

  // Date filter states
  const [dateRangeMode, setDateRangeMode] = useState<
    'preset' | 'specific' | 'custom'
  >('preset');
  const [datePreset, setDatePreset] = useState('last7days');
  const [specificPeriod, setSpecificPeriod] = useState<
    'date' | 'month' | 'year'
  >('date');
  const [specificValue, setSpecificValue] = useState(
    dayjs().format('YYYY-MM-DD')
  );
  const [fromDate, setFromDate] = useState<string | undefined>();
  const [toDate, setToDate] = useState<string | undefined>();

  // API date values
  const [apiStartTime, setApiStartTime] = useState<string | undefined>();
  const [apiEndTime, setApiEndTime] = useState<string | undefined>();

  // Get selected remote node
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  // Track previous values to detect filter changes
  const prevCategoryRef = useRef(category);
  const prevRemoteNodeRef = useRef(remoteNode);
  const prevApiStartTimeRef = useRef(apiStartTime);
  const prevApiEndTimeRef = useRef(apiEndTime);
  const structuredFilterSignature = [
    source,
    surface,
    result,
    action,
    workspace,
    credentialId,
    correlationId,
    resourceId,
    mcpTool,
  ].join('\u0000');
  const prevStructuredFilterRef = useRef(structuredFilterSignature);

  // Helper functions for date calculations
  const getPresetDates = useCallback(
    (preset: string): { from: string; to?: string } => {
      const now = dayjs();
      const startOfDay =
        config.tzOffsetInSec !== undefined
          ? now.utcOffset(config.tzOffsetInSec / 60).startOf('day')
          : now.startOf('day');

      switch (preset) {
        case 'today':
          return { from: startOfDay.format('YYYY-MM-DDTHH:mm:ss') };
        case 'yesterday':
          return {
            from: startOfDay.subtract(1, 'day').format('YYYY-MM-DDTHH:mm:ss'),
            to: startOfDay.format('YYYY-MM-DDTHH:mm:ss'),
          };
        case 'last7days':
          return {
            from: startOfDay.subtract(7, 'day').format('YYYY-MM-DDTHH:mm:ss'),
          };
        case 'last30days':
          return {
            from: startOfDay.subtract(30, 'day').format('YYYY-MM-DDTHH:mm:ss'),
          };
        case 'thisWeek':
          return {
            from: startOfDay.startOf('week').format('YYYY-MM-DDTHH:mm:ss'),
          };
        case 'thisMonth':
          return {
            from: startOfDay.startOf('month').format('YYYY-MM-DDTHH:mm:ss'),
          };
        default:
          return {
            from: startOfDay.subtract(7, 'day').format('YYYY-MM-DDTHH:mm:ss'),
          };
      }
    },
    [config.tzOffsetInSec]
  );

  const getSpecificPeriodDates = useCallback(
    (
      period: 'date' | 'month' | 'year',
      value: string
    ): { from: string; to?: string } => {
      const parsedDate = dayjs(value);
      if (!parsedDate.isValid()) {
        const fallback =
          config.tzOffsetInSec !== undefined
            ? dayjs().utcOffset(config.tzOffsetInSec / 60)
            : dayjs();
        return { from: fallback.startOf('day').format('YYYY-MM-DDTHH:mm:ss') };
      }

      // Apply config timezone offset before calculating day/month/year boundaries.
      // This follows the same pattern as Dashboard (ui/src/pages/index.tsx).
      const date =
        config.tzOffsetInSec !== undefined
          ? parsedDate.utcOffset(config.tzOffsetInSec / 60)
          : parsedDate;

      // dayjs uses 'day' instead of 'date' for startOf/endOf
      const unit = period === 'date' ? 'day' : period;
      return {
        from: date.startOf(unit).format('YYYY-MM-DDTHH:mm:ss'),
        to: date.endOf(unit).format('YYYY-MM-DDTHH:mm:ss'),
      };
    },
    [config.tzOffsetInSec]
  );

  // Convert datetime to ISO 8601 for API calls
  const formatDateForApi = useCallback(
    (dateString: string | undefined): string | undefined => {
      if (!dateString) return undefined;
      // Add seconds if missing
      const dateWithSeconds =
        dateString.split(':').length < 3 ? `${dateString}:00` : dateString;
      // Apply timezone offset and convert to ISO string
      if (config.tzOffsetInSec !== undefined) {
        return dayjs(dateWithSeconds)
          .utcOffset(config.tzOffsetInSec / 60, true)
          .toISOString();
      }
      return dayjs(dateWithSeconds).toISOString();
    },
    [config.tzOffsetInSec]
  );

  const getInputTypeForPeriod = (period: 'date' | 'month' | 'year'): string => {
    switch (period) {
      case 'date':
        return 'date';
      case 'month':
        return 'month';
      case 'year':
        return 'number';
    }
  };

  // Format timezone offset for display
  const formatTimezoneOffset = (): string => {
    if (config.tzOffsetInSec === undefined) return '';
    const offsetInMinutes = config.tzOffsetInSec / 60;
    const hours = Math.floor(Math.abs(offsetInMinutes) / 60);
    const minutes = Math.abs(offsetInMinutes) % 60;
    const sign = offsetInMinutes >= 0 ? '+' : '-';
    return `(${sign}${hours.toString().padStart(2, '0')}:${minutes.toString().padStart(2, '0')})`;
  };

  const tzLabel = formatTimezoneOffset();

  // Initialize date values on mount
  useEffect(() => {
    const dates = getPresetDates('last7days');
    setFromDate(dates.from);
    setToDate(dates.to);
    setApiStartTime(formatDateForApi(dates.from));
    setApiEndTime(formatDateForApi(dates.to));
  }, [getPresetDates, formatDateForApi]);

  // Set page title on mount
  useEffect(() => {
    appBarContext.setTitle('Audit Logs');
  }, []);

  const fetchAuditLogs = useCallback(
    async (resetOffset = false) => {
      // Reset offset if filters changed
      let effectiveOffset = offset;
      const filtersChanged =
        prevCategoryRef.current !== category ||
        prevRemoteNodeRef.current !== remoteNode ||
        prevApiStartTimeRef.current !== apiStartTime ||
        prevApiEndTimeRef.current !== apiEndTime ||
        prevStructuredFilterRef.current !== structuredFilterSignature;

      if (resetOffset || filtersChanged) {
        effectiveOffset = 0;
        if (filtersChanged) {
          setOffset(0);
          prevCategoryRef.current = category;
          prevRemoteNodeRef.current = remoteNode;
          prevApiStartTimeRef.current = apiStartTime;
          prevApiEndTimeRef.current = apiEndTime;
          prevStructuredFilterRef.current = structuredFilterSignature;
        }
      }

      try {
        setIsLoading(true);
        const token = localStorage.getItem(TOKEN_KEY);

        const params = new URLSearchParams();
        params.set('remoteNode', remoteNode);
        if (category && category !== 'all') params.set('category', category);
        if (source && source !== 'all') params.set('source', source);
        if (surface && surface !== 'all') params.set('surface', surface);
        if (result && result !== 'all') params.set('result', result);
        if (action.trim()) params.set('action', action.trim());
        if (workspace.trim()) params.set('workspace', workspace.trim());
        if (credentialId.trim())
          params.set('credentialId', credentialId.trim());
        if (correlationId.trim())
          params.set('correlationId', correlationId.trim());
        if (resourceId.trim()) params.set('resourceId', resourceId.trim());
        if (mcpTool.trim()) params.set('mcpTool', mcpTool.trim());
        params.set('limit', String(PAGE_SIZE));
        params.set('offset', String(effectiveOffset));
        if (apiStartTime) params.set('startTime', apiStartTime);
        if (apiEndTime) params.set('endTime', apiEndTime);

        const response = await fetch(
          `${config.apiURL}/audit?${params.toString()}`,
          {
            headers: {
              Authorization: `Bearer ${token}`,
            },
          }
        );

        if (!response.ok) {
          if (response.status === 403) {
            throw new Error('You do not have permission to view audit logs');
          }
          throw new Error('Failed to fetch audit logs');
        }

        const data = await response.json();
        setEntries(data.entries || []);
        setTotal(data.total || 0);
        setError(null);
      } catch (err) {
        setError(
          err instanceof Error ? err.message : 'Failed to load audit logs'
        );
      } finally {
        setIsLoading(false);
      }
    },
    [
      config.apiURL,
      category,
      source,
      surface,
      result,
      action,
      workspace,
      credentialId,
      correlationId,
      resourceId,
      mcpTool,
      structuredFilterSignature,
      offset,
      remoteNode,
      apiStartTime,
      apiEndTime,
    ]
  );

  useEffect(() => {
    if (apiStartTime !== undefined) {
      fetchAuditLogs();
    }
  }, [fetchAuditLogs, apiStartTime]);

  const handlePreviousPage = () => {
    setOffset(Math.max(0, offset - PAGE_SIZE));
  };

  const handleNextPage = () => {
    if (offset + PAGE_SIZE < total) {
      setOffset(offset + PAGE_SIZE);
    }
  };

  const handleDatePresetChange = (preset: string) => {
    setDatePreset(preset);
    const dates = getPresetDates(preset);
    setFromDate(dates.from);
    setToDate(dates.to);
    setApiStartTime(formatDateForApi(dates.from));
    setApiEndTime(formatDateForApi(dates.to));
  };

  const handleSpecificPeriodChange = (
    value: string,
    period?: 'date' | 'month' | 'year'
  ) => {
    setSpecificValue(value);
    const periodToUse = period || specificPeriod;
    const dates = getSpecificPeriodDates(periodToUse, value);
    setFromDate(dates.from);
    setToDate(dates.to);
    setApiStartTime(formatDateForApi(dates.from));
    setApiEndTime(formatDateForApi(dates.to));
  };

  const handleDateRangeModeChange = (
    newMode: 'preset' | 'specific' | 'custom'
  ) => {
    setDateRangeMode(newMode);

    if (newMode === 'preset') {
      const dates = getPresetDates(datePreset);
      setFromDate(dates.from);
      setToDate(dates.to);
      setApiStartTime(formatDateForApi(dates.from));
      setApiEndTime(formatDateForApi(dates.to));
    } else if (newMode === 'specific') {
      const dates = getSpecificPeriodDates(specificPeriod, specificValue);
      setFromDate(dates.from);
      setToDate(dates.to);
      setApiStartTime(formatDateForApi(dates.from));
      setApiEndTime(formatDateForApi(dates.to));
    }
  };

  const handleCustomDateSearch = () => {
    setApiStartTime(formatDateForApi(fromDate));
    setApiEndTime(formatDateForApi(toDate));
  };

  const applyQuickFilter = (
    filter: 'all' | 'mcp' | 'rest' | 'failed' | 'denied'
  ) => {
    setCategory(filter === 'mcp' ? 'mcp' : 'all');
    setSource(filter === 'mcp' ? 'mcp' : filter === 'rest' ? 'rest' : 'all');
    setSurface(
      filter === 'mcp' ? 'mcp' : filter === 'rest' ? 'rest_api' : 'all'
    );
    setResult(
      filter === 'failed' ? 'failed' : filter === 'denied' ? 'denied' : 'all'
    );
    setAction('');
    setWorkspace('');
    setCredentialId('');
    setCorrelationId('');
    setResourceId('');
    setMcpTool('');
  };

  const currentPage = Math.floor(offset / PAGE_SIZE) + 1;
  const totalPages = Math.ceil(total / PAGE_SIZE);

  if (!canViewAuditLogs) {
    return (
      <div className="flex items-center justify-center h-64">
        <p className="text-muted-foreground">
          You do not have permission to access this page.
        </p>
      </div>
    );
  }

  const parseDetails = (
    details: string | undefined
  ): Record<string, unknown> => {
    if (!details) return {};
    try {
      return JSON.parse(details);
    } catch {
      return { raw: details };
    }
  };

  const formatDetails = (entry: AuditEntry): string => {
    const details = parseDetails(entry.details);
    if (entry.category === 'terminal') {
      if (
        entry.action === 'connection_start' ||
        entry.action === 'connection_end'
      ) {
        return `Connection: ${details.connection_id || 'N/A'}${details.reason ? ` (${details.reason})` : ''}`;
      }
      if (entry.action === 'command') {
        return `Command: ${details.command || 'N/A'}`;
      }
    }
    if (entry.category === 'agent') {
      if (entry.action === 'bash_exec')
        return `Command: ${details.command || 'N/A'}`;
      if (entry.action === 'file_read') return `Path: ${details.path || 'N/A'}`;
      if (entry.action === 'file_patch')
        return `${details.operation || 'patch'}: ${details.path || 'N/A'}`;
    }
    if (entry.category === 'mcp') {
      const summary = [
        entry.mcpTool || details.mcp_tool,
        entry.result || details.result,
        entry.resourceType || details.resource_type,
        entry.resourceId || details.resource_id,
      ]
        .filter(Boolean)
        .join(' / ');
      return summary || entry.details || '-';
    }
    return entry.details || '-';
  };

  const resultVariant = (value?: string) => {
    if (value === 'succeeded') return 'success';
    if (value === 'failed') return 'error';
    if (value === 'denied') return 'warning';
    return 'outline';
  };

  const quickFilterValue =
    category === 'mcp' && source === 'mcp' && surface === 'mcp'
      ? 'mcp'
      : source === 'rest' && surface === 'rest_api'
        ? 'rest'
        : result === 'failed'
          ? 'failed'
          : result === 'denied'
            ? 'denied'
            : 'all';

  return (
    <div className="flex flex-col gap-4 max-w-7xl h-full">
      <div className="flex items-center justify-between flex-shrink-0">
        <div>
          <h1 className="text-lg font-semibold">Audit Logs</h1>
          <p className="text-sm text-muted-foreground">
            View system activity and security events
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={category} onValueChange={setCategory}>
            <SelectTrigger className="w-[160px] h-8">
              <SelectValue placeholder="All Categories" />
            </SelectTrigger>
            <SelectContent>
              {CATEGORIES.map((cat) => (
                <SelectItem key={cat.value} value={cat.value}>
                  {cat.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            onClick={() => fetchAuditLogs()}
            size="sm"
            variant="outline"
            className="h-8"
          >
            <RefreshCw className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {/* Date Filter Row */}
      <div className="flex flex-wrap items-center gap-2 flex-shrink-0">
        <ToggleGroup aria-label="Date range mode">
          <ToggleButton
            value="preset"
            groupValue={dateRangeMode}
            onClick={() => handleDateRangeModeChange('preset')}
            position="first"
            aria-label="Quick select"
          >
            Quick
          </ToggleButton>
          <ToggleButton
            value="specific"
            groupValue={dateRangeMode}
            onClick={() => handleDateRangeModeChange('specific')}
            position="middle"
            aria-label="Specific date/month/year"
          >
            Specific
          </ToggleButton>
          <ToggleButton
            value="custom"
            groupValue={dateRangeMode}
            onClick={() => handleDateRangeModeChange('custom')}
            position="last"
            aria-label="Custom range"
          >
            Custom
          </ToggleButton>
        </ToggleGroup>

        {dateRangeMode === 'preset' ? (
          <Select value={datePreset} onValueChange={handleDatePresetChange}>
            <SelectTrigger className="w-[180px] h-8">
              <SelectValue placeholder="Select period" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="today">Today</SelectItem>
              <SelectItem value="yesterday">Yesterday</SelectItem>
              <SelectItem value="last7days">Last 7 days</SelectItem>
              <SelectItem value="last30days">Last 30 days</SelectItem>
              <SelectItem value="thisWeek">This week</SelectItem>
              <SelectItem value="thisMonth">This month</SelectItem>
            </SelectContent>
          </Select>
        ) : dateRangeMode === 'specific' ? (
          <>
            <Select
              value={specificPeriod}
              onValueChange={(v) => {
                const newPeriod = v as 'date' | 'month' | 'year';
                setSpecificPeriod(newPeriod);
                let newValue: string;
                const parsedDate = dayjs(specificValue);

                if (newPeriod === 'date') {
                  newValue = parsedDate.isValid()
                    ? parsedDate.format('YYYY-MM-DD')
                    : dayjs().format('YYYY-MM-DD');
                } else if (newPeriod === 'month') {
                  newValue = parsedDate.isValid()
                    ? parsedDate.format('YYYY-MM')
                    : dayjs().format('YYYY-MM');
                } else {
                  newValue = parsedDate.isValid()
                    ? parsedDate.format('YYYY')
                    : dayjs().format('YYYY');
                }

                setSpecificValue(newValue);
                handleSpecificPeriodChange(newValue, newPeriod);
              }}
            >
              <SelectTrigger className="w-[100px] h-8">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="date">Date</SelectItem>
                <SelectItem value="month">Month</SelectItem>
                <SelectItem value="year">Year</SelectItem>
              </SelectContent>
            </Select>
            <Input
              type={getInputTypeForPeriod(specificPeriod)}
              value={specificValue}
              onChange={(e) => handleSpecificPeriodChange(e.target.value)}
              placeholder={specificPeriod === 'year' ? 'YYYY' : undefined}
              min={specificPeriod === 'year' ? '2000' : undefined}
              max={specificPeriod === 'year' ? '2100' : undefined}
              className="w-[140px] h-8"
            />
          </>
        ) : (
          <>
            <DateRangePicker
              fromDate={fromDate}
              toDate={toDate}
              onFromDateChange={setFromDate}
              onToDateChange={setToDate}
              onEnterPress={handleCustomDateSearch}
              fromLabel={`From ${tzLabel}`}
              toLabel={`To ${tzLabel}`}
              className="w-full md:w-auto"
            />
            <Button onClick={handleCustomDateSearch} size="sm" className="h-8">
              Apply
            </Button>
          </>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2 flex-shrink-0">
        <ToggleGroup aria-label="Audit quick filters">
          <ToggleButton
            value="all"
            groupValue={quickFilterValue}
            onClick={() => applyQuickFilter('all')}
            position="first"
            aria-label="All audit entries"
          >
            All
          </ToggleButton>
          <ToggleButton
            value="mcp"
            groupValue={quickFilterValue}
            onClick={() => applyQuickFilter('mcp')}
            position="middle"
            aria-label="MCP audit entries"
          >
            MCP
          </ToggleButton>
          <ToggleButton
            value="rest"
            groupValue={quickFilterValue}
            onClick={() => applyQuickFilter('rest')}
            position="middle"
            aria-label="REST audit entries"
          >
            REST
          </ToggleButton>
          <ToggleButton
            value="failed"
            groupValue={quickFilterValue}
            onClick={() => applyQuickFilter('failed')}
            position="middle"
            aria-label="Failed audit entries"
          >
            Failed
          </ToggleButton>
          <ToggleButton
            value="denied"
            groupValue={quickFilterValue}
            onClick={() => applyQuickFilter('denied')}
            position="last"
            aria-label="Denied audit entries"
          >
            Denied
          </ToggleButton>
        </ToggleGroup>

        <Select value={source} onValueChange={setSource}>
          <SelectTrigger className="w-[140px] h-8">
            <SelectValue placeholder="Source" />
          </SelectTrigger>
          <SelectContent>
            {SOURCES.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select value={surface} onValueChange={setSurface}>
          <SelectTrigger className="w-[150px] h-8">
            <SelectValue placeholder="Surface" />
          </SelectTrigger>
          <SelectContent>
            {SURFACES.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select value={result} onValueChange={setResult}>
          <SelectTrigger className="w-[140px] h-8">
            <SelectValue placeholder="Result" />
          </SelectTrigger>
          <SelectContent>
            {RESULTS.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Input
          value={action}
          onChange={(e) => setAction(e.target.value)}
          placeholder="Action"
          className="w-[170px] h-8"
        />
        <Input
          value={workspace}
          onChange={(e) => setWorkspace(e.target.value)}
          placeholder="Workspace"
          className="w-[140px] h-8"
        />
        <Input
          value={credentialId}
          onChange={(e) => setCredentialId(e.target.value)}
          placeholder="Credential ID"
          className="w-[170px] h-8"
        />
        <Input
          value={correlationId}
          onChange={(e) => setCorrelationId(e.target.value)}
          placeholder="Correlation ID"
          className="w-[180px] h-8"
        />
        <Input
          value={resourceId}
          onChange={(e) => setResourceId(e.target.value)}
          placeholder="Resource ID"
          className="w-[160px] h-8"
        />
        <Input
          value={mcpTool}
          onChange={(e) => setMcpTool(e.target.value)}
          placeholder="MCP tool"
          className="w-[140px] h-8"
        />
      </div>

      {error && (
        <div className="p-3 text-sm text-destructive bg-destructive/10 rounded-md flex-shrink-0">
          {error}
        </div>
      )}

      <div className="border border-border rounded-md flex-1 min-h-0 flex flex-col bg-background overflow-hidden">
        <div className="flex-shrink-0 border-b border-border bg-background">
          <table className="w-full table-fixed bg-background">
            <thead>
              <tr>
                <th className="w-[180px] px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                  Timestamp
                </th>
                <th className="w-[100px] px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                  Category
                </th>
                <th className="w-[120px] px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                  Action
                </th>
                <th className="w-[90px] px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                  Source
                </th>
                <th className="w-[100px] px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                  Result
                </th>
                <th className="w-[120px] px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                  User
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                  Details
                </th>
                <th className="w-[120px] px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                  IP Address
                </th>
              </tr>
            </thead>
          </table>
        </div>
        <div className="flex-1 min-h-0 overflow-auto bg-background">
          <table className="w-full table-fixed bg-background">
            <tbody>
              {isLoading ? (
                <tr>
                  <td
                    colSpan={8}
                    className="text-center text-muted-foreground py-8"
                  >
                    Loading audit logs...
                  </td>
                </tr>
              ) : entries.length === 0 ? (
                <tr>
                  <td
                    colSpan={8}
                    className="text-center text-muted-foreground py-8"
                  >
                    <ScrollText className="h-8 w-8 mx-auto mb-2 opacity-50" />
                    No audit log entries found
                  </td>
                </tr>
              ) : (
                entries.map((entry) => (
                  <tr
                    key={entry.id}
                    className="border-b border-border bg-background hover:bg-muted/50"
                  >
                    <td className="w-[180px] px-4 py-3 text-sm text-muted-foreground whitespace-nowrap">
                      {config.tzOffsetInSec !== undefined
                        ? dayjs(entry.timestamp)
                            .utcOffset(config.tzOffsetInSec / 60)
                            .format('MMM D, YYYY HH:mm:ss')
                        : dayjs(entry.timestamp).format('MMM D, YYYY HH:mm:ss')}
                    </td>
                    <td className="w-[100px] px-4 py-3">
                      <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground capitalize">
                        {entry.category}
                      </span>
                    </td>
                    <td className="w-[120px] px-4 py-3">
                      <span className="text-xs font-mono">{entry.action}</span>
                    </td>
                    <td className="w-[90px] px-4 py-3">
                      {entry.source ? (
                        <Badge variant="outline">{entry.source}</Badge>
                      ) : (
                        <span className="text-sm text-muted-foreground">-</span>
                      )}
                    </td>
                    <td className="w-[100px] px-4 py-3">
                      {entry.result ? (
                        <Badge variant={resultVariant(entry.result)}>
                          {entry.result}
                        </Badge>
                      ) : (
                        <span className="text-sm text-muted-foreground">-</span>
                      )}
                    </td>
                    <td className="w-[120px] px-4 py-3 text-sm">
                      {entry.username}
                    </td>
                    <td
                      className="px-4 py-3 text-sm text-muted-foreground truncate"
                      title={entry.details}
                    >
                      {formatDetails(entry)}
                    </td>
                    <td className="w-[120px] px-4 py-3 text-sm text-muted-foreground font-mono">
                      {entry.ipAddress || '-'}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Pagination */}
      {total > PAGE_SIZE && (
        <div className="flex items-center justify-between flex-shrink-0">
          <p className="text-sm text-muted-foreground">
            Showing {offset + 1} - {Math.min(offset + PAGE_SIZE, total)} of{' '}
            {total} entries
          </p>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={handlePreviousPage}
              disabled={offset === 0}
              className="h-8"
            >
              <ChevronLeft className="h-4 w-4 mr-1" />
              Previous
            </Button>
            <span className="text-sm text-muted-foreground">
              Page {currentPage} of {totalPages}
            </span>
            <Button
              variant="outline"
              size="sm"
              onClick={handleNextPage}
              disabled={offset + PAGE_SIZE >= total}
              className="h-8"
            >
              Next
              <ChevronRight className="h-4 w-4 ml-1" />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
