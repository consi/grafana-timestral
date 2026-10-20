import React, { useMemo } from 'react';
import { css } from '@emotion/css';
import {
  AbsoluteTimeRange,
  DataFrame,
  dateTime,
  FieldColorModeId,
  FieldConfigSource,
  FieldMatcherID,
  getFieldDisplayName,
  GrafanaTheme2,
  LoadingState,
  PanelData,
  ThresholdsMode,
} from '@grafana/data';
import { PanelRenderer } from '@grafana/runtime';
import {
  GraphDrawStyle,
  GraphFieldConfig,
  GraphThresholdsStyleMode,
  LineInterpolation,
  VisibilityMode,
} from '@grafana/schema';
import { Combobox, Icon, InlineSwitch, Spinner, Tooltip, useStyles2, useTheme2 } from '@grafana/ui';
import { forecastNames, padOutsideWithNulls } from './forecast';
import { ForecastSettings, QueryInfo } from './ForecastSettings';
import { BAND_OPTIONS, BANDS, ForecastOptions, HORIZON_PRESETS } from './types';
import { ForecastState, useForecast } from './useForecast';

const TOOLBAR_HEIGHT = 32;

export interface ForecastViewProps {
  /** Unique per rendered view; used to cancel superseded forecast requests. */
  id: number | string;
  title: string;
  data: PanelData;
  options: ForecastOptions;
  onOptionsChange: (options: ForecastOptions) => void;
  fieldConfig?: FieldConfigSource;
  width: number;
  height: number;
  timeZone?: string;
  onChangeTimeRange?: (range: AbsoluteTimeRange) => void;
}

const emptyFieldConfig: FieldConfigSource = { defaults: {}, overrides: [] };

/** Native time series rendering of `data` plus a Timestral forecast; shared by the panel and the modals. */
export const ForecastView = (props: ForecastViewProps) => {
  const { id, data, options, width, height, timeZone, onOptionsChange, onChangeTimeRange, title } = props;
  const fieldConfig = props.fieldConfig ?? emptyFieldConfig;
  const styles = useStyles2(getStyles);
  const theme = useTheme2();
  const forecast = useForecast(id, data.series, data.timeRange, timeZone, options);

  // Queries present in the data, labelled with their first series, for the inputs picker.
  const queries = useMemo<QueryInfo[]>(() => {
    const seen = new Map<string, string>();
    for (const frame of data.series) {
      const field = frame.fields.find((f) => f.type === 'number');
      if (frame.refId && field && !seen.has(frame.refId)) {
        seen.set(frame.refId, getFieldDisplayName(field, frame, data.series));
      }
    }
    return [...seen].map(([refId, description]) => ({ refId, description }));
  }, [data.series]);

  // Clone source series with a fixed per-series color so the forecast can reuse it.
  const { frames, colors } = useMemo(() => {
    const palette = theme.visualization.palette;
    const colors = new Map<string, string>();
    let i = 0;
    const frames: DataFrame[] = data.series.map((frame) => ({
      ...frame,
      fields: frame.fields.map((field) => {
        if (field.type !== 'number') {
          return field;
        }
        const name = getFieldDisplayName(field, frame, data.series);
        const color =
          field.config.color?.mode === FieldColorModeId.Fixed && field.config.color?.fixedColor
            ? theme.visualization.getColorByName(field.config.color.fixedColor)
            : theme.visualization.getColorByName(palette[i % palette.length]);
        i++;
        colors.set(name, color);
        return { ...field, config: { ...field.config, displayName: name } };
      }),
    }));
    return { frames, colors };
  }, [data.series, theme]);

  const rendererFieldConfig = useMemo<FieldConfigSource<Partial<GraphFieldConfig>>>(() => {
    const overrides: FieldConfigSource['overrides'] = [];
    const fixed = (c: string) => ({ id: 'color', value: { mode: FieldColorModeId.Fixed, fixedColor: c } });
    const byName = (options: string) => ({ id: FieldMatcherID.byName, options });
    const bands = options.band === 'none' ? [] : BANDS[options.band];
    const bandOpacity = options.band === 'fan' ? Math.round(options.fillOpacity * 0.6) : options.fillOpacity;
    for (const [name, color] of colors) {
      overrides.push({ matcher: byName(name), properties: [fixed(color)] });
      const n = forecastNames(name);
      overrides.push({
        matcher: byName(n.median),
        properties: [
          fixed(color),
          { id: 'custom.lineStyle', value: { fill: 'dash', dash: [10, 6] } },
          { id: 'custom.lineWidth', value: 2 },
          { id: 'custom.fillOpacity', value: 0 },
        ],
      });
      const bandProps = [
        fixed(color),
        { id: 'custom.lineWidth', value: 0 },
        { id: 'custom.hideFrom', value: { legend: true, tooltip: false, viz: false } },
      ];
      for (const [lo, hi] of bands) {
        const b = n.band(lo, hi);
        overrides.push({
          matcher: byName(b.lower),
          properties: [...bandProps, { id: 'custom.fillOpacity', value: 0 }],
        });
        overrides.push({
          matcher: byName(b.upper),
          properties: [
            ...bandProps,
            { id: 'custom.fillBelowTo', value: b.lower },
            { id: 'custom.fillOpacity', value: bandOpacity },
          ],
        });
      }
      overrides.push({
        matcher: byName(n.anomalies),
        properties: [
          fixed(theme.colors.error.main),
          { id: 'custom.drawStyle', value: GraphDrawStyle.Points },
          { id: 'custom.pointSize', value: 8 },
          { id: 'custom.showPoints', value: VisibilityMode.Always },
        ],
      });
    }
    // Covariates usually have other units (e.g. bytes/s vs %), so give them their own axis.
    for (const [refId, role] of Object.entries(options.roles)) {
      if (role === 'target' || role === 'ignore') {
        continue;
      }
      overrides.push({
        matcher: { id: FieldMatcherID.byFrameRefID, options: refId },
        properties: [
          { id: 'custom.axisPlacement', value: 'right' },
          { id: 'custom.lineStyle', value: { fill: 'dot', dash: [2, 4] } },
          { id: 'custom.axisLabel', value: 'covariates' },
        ],
      });
    }
    const defaults = {
      ...fieldConfig.defaults,
      custom: {
        lineInterpolation: LineInterpolation.Smooth,
        lineWidth: 1,
        fillOpacity: 0,
        ...fieldConfig.defaults.custom,
      },
    };
    if (options.threshold != null && !Number.isNaN(options.threshold)) {
      // An override, not defaults: the incoming fields already carry the panel's thresholds.
      overrides.push({
        matcher: { id: FieldMatcherID.numeric, options: {} },
        properties: [
          {
            id: 'thresholds',
            value: {
              mode: ThresholdsMode.Absolute,
              steps: [
                { value: -Infinity, color: 'transparent' },
                { value: options.threshold, color: theme.colors.error.main },
              ],
            },
          },
          { id: 'custom.thresholdsStyle', value: { mode: GraphThresholdsStyleMode.Dashed } },
        ],
      });
    }
    return { defaults, overrides: [...overrides, ...fieldConfig.overrides] };
  }, [colors, fieldConfig, options.band, options.fillOpacity, options.roles, options.threshold, theme]);

  // Extend the x-axis so the forecast is visible after the end of the dashboard range.
  const panelData = useMemo<PanelData>(() => {
    const series = padOutsideWithNulls([...frames, ...forecast.frames]);
    if (!forecast.horizonEnd || forecast.horizonEnd <= data.timeRange.to.valueOf()) {
      return { ...data, series };
    }
    const to = dateTime(forecast.horizonEnd);
    return {
      ...data,
      series,
      timeRange: { from: data.timeRange.from, to, raw: { from: data.timeRange.from, to } },
    };
  }, [data, frames, forecast.frames, forecast.horizonEnd]);

  const toolbarHeight = options.showToolbar ? TOOLBAR_HEIGHT : 0;
  const update = (patch: Partial<ForecastOptions>) => onOptionsChange({ ...options, ...patch });
  const horizonOptions = HORIZON_PRESETS.includes(options.horizon)
    ? HORIZON_PRESETS
    : [...HORIZON_PRESETS, options.horizon];

  return (
    <div className={styles.wrapper}>
      {options.showToolbar && (
        <div className={styles.toolbar} style={{ height: TOOLBAR_HEIGHT }}>
          <InlineSwitch
            label="Forecast"
            showLabel
            transparent
            value={options.enabled}
            onChange={(e) => update({ enabled: e.currentTarget.checked })}
          />
          <Combobox
            width={12}
            aria-label="Forecast horizon"
            disabled={!options.enabled}
            value={options.horizon}
            options={horizonOptions.map((h) => ({ label: h === 'auto' ? 'Auto' : `+${h}`, value: h }))}
            onChange={(v) => update({ horizon: v.value! })}
            createCustomValue
          />
          <Combobox
            width={16}
            aria-label="Confidence band"
            disabled={!options.enabled}
            value={options.band}
            options={BAND_OPTIONS}
            onChange={(v) => update({ band: v.value! })}
          />
          <ForecastSettings options={options} queries={queries} onChange={update} />
          <Status forecast={forecast} options={options} />
        </div>
      )}
      <PanelRenderer
        pluginId="timeseries"
        title={title}
        data={{ ...panelData, state: data.state ?? LoadingState.Done }}
        options={{
          legend: { showLegend: true, displayMode: 'list', placement: 'bottom', calcs: [] },
          tooltip: { mode: 'multi', sort: 'none' },
        }}
        fieldConfig={rendererFieldConfig}
        timeZone={timeZone}
        width={width}
        height={height - toolbarHeight}
        onChangeTimeRange={onChangeTimeRange}
      />
    </div>
  );
};

function formatDuration(ms: number): string {
  const m = Math.round(ms / 60_000);
  const d = Math.floor(m / 1440);
  const h = Math.floor((m % 1440) / 60);
  const min = m % 60;
  if (d) {
    return h ? `${d}d ${h}h` : `${d}d`;
  }
  if (h) {
    return min ? `${h}h ${min}m` : `${h}h`;
  }
  return `${min}m`;
}

const pct = (x?: number) => (x === undefined ? '–' : `${(x * 100).toFixed(x < 0.1 ? 1 : 0)}%`);

/** Status line: model info, threshold ETA, backtest accuracy or the error. */
const Status = ({ forecast, options }: { forecast: ForecastState; options: ForecastOptions }) => {
  const styles = useStyles2(getStyles);
  if (forecast.loading && !forecast.info) {
    return (
      <div className={styles.status}>
        <Spinner inline />
      </div>
    );
  }
  if (forecast.error) {
    return (
      <div className={styles.status}>
        <Tooltip content={forecast.error}>
          <span className={styles.error}>
            <Icon name="exclamation-triangle" /> {forecast.error.slice(0, 80)}
          </span>
        </Tooltip>
      </div>
    );
  }
  if (!options.enabled || !forecast.info) {
    return null;
  }

  const parts: React.ReactNode[] = [];
  const details: string[] = [forecast.info];
  const now = forecast.computedAt ?? 0;
  if (forecast.crossings.length) {
    const word = options.thresholdDirection === 'above' ? 'above' : 'below';
    const hits = forecast.crossings.filter((c) => c.median !== undefined || c.early !== undefined);
    if (hits.length) {
      const soonest = hits.reduce((a, b) => ((a.early ?? a.median!) <= (b.early ?? b.median!) ? a : b));
      const when = (t?: number) =>
        t === undefined ? 'not in horizon' : t <= now ? 'now' : `in ${formatDuration(t - now)}`;
      const early =
        soonest.early !== undefined ? `p${Math.round((soonest.earlyLevel ?? 0) * 100)} ${when(soonest.early)}` : '';
      const med = soonest.median !== undefined ? `median ${when(soonest.median)}` : 'median stays within';
      parts.push(
        <span key="eta" className={styles.warning}>
          <Icon name="exclamation-triangle" /> {soonest.name} {word} {options.threshold}:{' '}
          {[early, med].filter(Boolean).join(', ')}
        </span>
      );
      hits.forEach((c) => details.push(`${c.name}: median ${when(c.median)}, band edge ${when(c.early)}`));
    } else {
      parts.push(
        <span key="eta">
          <Icon name="check" /> stays {word === 'above' ? 'below' : 'above'} {options.threshold}
        </span>
      );
    }
  }
  if (forecast.scores.length) {
    const s = forecast.scores;
    const points = s.reduce((n, x) => n + x.points, 0);
    const anomalies = s.reduce((n, x) => n + x.anomalies, 0);
    const covered = s.filter((x) => x.coverage !== undefined);
    const coverage = covered.length
      ? covered.reduce((n, x) => n + x.coverage! * x.points, 0) / covered.reduce((n, x) => n + x.points, 0)
      : undefined;
    const mapes = s.filter((x) => x.mape !== undefined);
    const mape = mapes.length ? mapes.reduce((n, x) => n + x.mape!, 0) / mapes.length : undefined;
    parts.push(
      <span key="bt">
        Backtest: coverage {pct(coverage)} · MAPE {pct(mape)} · {anomalies}/{points} outside
      </span>
    );
    s.forEach((x) =>
      details.push(
        `${x.name}: coverage ${pct(x.coverage)}, MAPE ${pct(x.mape)}, MAE ${x.mae.toPrecision(3)}, ${x.anomalies} anomalies`
      )
    );
  }
  if (!parts.length) {
    parts.push(<span key="info">{forecast.info}</span>);
  }
  return (
    <div className={styles.status}>
      {forecast.loading && <Spinner inline />}
      <Tooltip content={<div style={{ whiteSpace: 'pre-line' }}>{details.join('\n')}</div>}>
        <span>{parts.reduce<React.ReactNode[]>((acc, p, i) => (i ? [...acc, ' · ', p] : [p]), [])}</span>
      </Tooltip>
    </div>
  );
};

const getStyles = (theme: GrafanaTheme2) => ({
  wrapper: css({ display: 'flex', flexDirection: 'column', height: '100%' }),
  toolbar: css({
    display: 'flex',
    '& > *:not(:last-child)': { flexShrink: 0 },
    alignItems: 'center',
    gap: theme.spacing(1),
    flexShrink: 0,
    fontSize: theme.typography.bodySmall.fontSize,
  }),
  status: css({
    marginLeft: 'auto',
    minWidth: 0,
    color: theme.colors.text.secondary,
    whiteSpace: 'nowrap',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
  }),
  error: css({ color: theme.colors.error.text }),
  warning: css({ color: theme.colors.warning.text }),
});
