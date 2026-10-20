import React, { useEffect, useMemo, useRef, useState } from 'react';
import { css } from '@emotion/css';
import { dateTime, FieldType, PanelData, TimeRange } from '@grafana/data';
import { usePluginComponent } from '@grafana/runtime';
import { DataQuery } from '@grafana/schema';
import { Alert, Button, Stack, useTheme2 } from '@grafana/ui';
import { PANEL_ID } from '../constants';
import { ForecastView } from '../panels/forecast/ForecastView';
import { DEFAULT_OPTIONS, ForecastOptions } from '../panels/forecast/types';

const CHART_HEIGHT = 460;

export function hasTimeSeries(data?: PanelData): boolean {
  return Boolean(
    data?.series.some(
      (f) => f.fields.some((x) => x.type === FieldType.time) && f.fields.some((x) => x.type === FieldType.number)
    )
  );
}

/**
 * Extension contexts are read-only proxies, but Grafana's field helpers cache into `field.state`,
 * so work on a mutable copy.
 */
function cloneData(data: PanelData): PanelData {
  const from = dateTime(data.timeRange.from.valueOf());
  const to = dateTime(data.timeRange.to.valueOf());
  return {
    state: data.state,
    series: data.series.map((frame) => ({
      ...frame,
      meta: frame.meta ? { ...frame.meta } : undefined,
      fields: frame.fields.map((field) => ({
        ...field,
        config: JSON.parse(JSON.stringify(field.config)),
        values: [...field.values],
        state: undefined,
      })),
    })),
    timeRange: { from, to, raw: { from, to } },
  };
}

/**
 * Chart tooltips live in the same portal container as modals but with a lower z-index, so they
 * render behind this modal. Lift them while the modal is open.
 */
function useTooltipAboveModal() {
  const theme = useTheme2();
  useEffect(() => {
    const style = document.createElement('style');
    style.textContent = `#grafana-portal-container > div:has(> [data-testid="data-testid viz-tooltip-wrapper"]) { z-index: ${
      theme.zIndex.modal + 1
    } !important; }`;
    document.head.appendChild(style);
    return () => style.remove();
  }, [theme]);
}

function useWidth() {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    if (!ref.current) {
      return;
    }
    const ro = new ResizeObserver(([entry]) => setWidth(Math.floor(entry.contentRect.width)));
    ro.observe(ref.current);
    return () => ro.disconnect();
  }, []);
  return { ref, width };
}

type AddToDashboardFormProps = {
  onClose: () => void;
  buildPanel: () => object;
  timeRange?: TimeRange;
};

interface Props {
  id: string;
  title: string;
  data?: PanelData;
  /** Queries behind `data`; needed to recreate the forecast as a dashboard panel. */
  targets?: DataQuery[];
  timeZone?: string;
  onDismiss?: () => void;
}

/** Forecast of a snapshot of query results, opened from the panel menu or the Explore toolbar. */
export const ForecastModal = ({ id, title, data, targets, timeZone, onDismiss }: Props) => {
  const [options, setOptions] = useState<ForecastOptions>(DEFAULT_OPTIONS);
  const { ref, width } = useWidth();
  useTooltipAboveModal();
  const [adding, setAdding] = useState(false);
  const { component: AddToDashboardForm } = usePluginComponent<AddToDashboardFormProps>(
    'grafana/add-to-dashboard-form/v1'
  );
  const queries = useMemo(() => JSON.parse(JSON.stringify(targets ?? [])) as DataQuery[], [targets]);
  const snapshot = useMemo(() => (data && hasTimeSeries(data) ? cloneData(data) : undefined), [data]);

  if (!snapshot) {
    return (
      <Alert severity="info" title="Nothing to forecast">
        Run a query that returns time series first.
      </Alert>
    );
  }

  return (
    <Stack direction="column" gap={2}>
      <div ref={ref} className={css({ width: '100%', height: CHART_HEIGHT })}>
        {width > 0 && (
          <ForecastView
            id={id}
            title={title}
            data={snapshot}
            options={options}
            onOptionsChange={setOptions}
            width={width}
            height={CHART_HEIGHT}
            timeZone={timeZone}
          />
        )}
      </div>
      {adding && AddToDashboardForm ? (
        <AddToDashboardForm
          onClose={() => setAdding(false)}
          timeRange={snapshot.timeRange}
          buildPanel={() => buildPanel(title, queries, options)}
        />
      ) : (
        <Stack justifyContent="flex-end">
          {AddToDashboardForm && queries.length > 0 && (
            <Button icon="apps" variant="primary" onClick={() => setAdding(true)}>
              Add to dashboard
            </Button>
          )}
          <Button variant="secondary" onClick={onDismiss}>
            Close
          </Button>
        </Stack>
      )}
    </Stack>
  );
};

/**
 * A Timestral Forecast panel running the same queries with the options chosen in the modal.
 * Queries without a data source run on the dashboard's default one.
 */
function buildPanel(title: string, targets: DataQuery[], options: ForecastOptions) {
  const uids = new Set(targets.map((t) => t.datasource?.uid).filter(Boolean));
  const datasource =
    uids.size === 0
      ? undefined
      : uids.size === 1
        ? targets.find((t) => t.datasource)?.datasource
        : { uid: '-- Mixed --' };
  return {
    type: PANEL_ID,
    title: title ? `${title} · forecast` : 'Forecast',
    datasource,
    targets,
    options,
    fieldConfig: { defaults: {}, overrides: [] },
  };
}
