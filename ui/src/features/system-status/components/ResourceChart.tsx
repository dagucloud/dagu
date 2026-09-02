import React from 'react';
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { components } from '../../../api/v1/schema';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { formatBytes } from '../../../lib/formatBytes';
import { I18nText } from '@/i18n/I18nText';

type MetricPoint = components['schemas']['MetricPoint'];

type ResourceChartProps = {
  title: string;
  data: MetricPoint[] | undefined;
  color: string;
  unit?: string;
  isLoading?: boolean;
  error?: string;
  totalBytes?: number;
  usedBytes?: number;
};

function ResourceChart({
  title,
  data,
  color,
  unit = '%',
  isLoading,
  error,
  totalBytes,
  usedBytes,
}: ResourceChartProps): React.ReactElement {
  if (error) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">{title}</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex h-[200px] items-center justify-center text-sm text-muted-foreground">
            <I18nText text={"Failed to load data"} />
          </div>
        </CardContent>
      </Card>
    );
  }

  if (isLoading) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">{title}</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="h-[200px] w-full bg-muted animate-pulse rounded-md" />
        </CardContent>
      </Card>
    );
  }

  const formattedData =
    data?.map((point) => ({
      time: point.timestamp
        ? new Date(point.timestamp * 1000).toLocaleTimeString()
        : '',
      value: point.value ?? 0,
    })) || [];

  const lastPoint = formattedData[formattedData.length - 1];
  const currentValue = lastPoint ? lastPoint.value : 0;
  const gradientId = `gradient-${title.replace(/\s+/g, '-')}`;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <div>
          <CardTitle className="text-sm font-medium">{title}</CardTitle>
          {totalBytes != null && usedBytes != null && totalBytes > 0 && (
            <p className="text-xs text-muted-foreground mt-0.5">
              {formatBytes(usedBytes)} / {formatBytes(totalBytes)}
            </p>
          )}
        </div>
        <div className="text-2xl font-bold">
          {currentValue.toFixed(1)}
          {unit}
        </div>
      </CardHeader>
      <CardContent>
        <div className="h-[200px] w-full">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart
              data={formattedData}
              margin={{
                top: 5,
                right: 0,
                left: 0,
                bottom: 0,
              }}
            >
              <defs>
                <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={color} stopOpacity={0.3} />
                  <stop offset="100%" stopColor={color} stopOpacity={0.05} />
                </linearGradient>
              </defs>
              <CartesianGrid
                strokeDasharray="3 3"
                vertical={false}
                stroke="var(--border)"
              />
              <XAxis dataKey="time" hide />
              <YAxis hide domain={[0, 'auto']} />
              <Tooltip
                contentStyle={{
                  backgroundColor: 'var(--background)',
                  border: '1px solid var(--border)',
                  borderRadius: 'var(--radius)',
                }}
                itemStyle={{ color: 'var(--foreground)' }}
                labelStyle={{ color: 'var(--muted-foreground)' }}
                formatter={(value: number | string) => [
                  `${Number(value).toFixed(1)}${unit}`,
                  title,
                ]}
              />
              <Area
                type="monotone"
                dataKey="value"
                stroke={color}
                strokeWidth={2}
                fill={`url(#${gradientId})`}
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </CardContent>
    </Card>
  );
}

export default ResourceChart;
