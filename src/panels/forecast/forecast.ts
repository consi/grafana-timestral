import { createDataFrame, DataFrame, Field, FieldType, getFieldDisplayName, rangeUtil, TimeRange } from '@grafana/data';
import { ForecastResponse, SeriesInput } from '../../api';
import {
  BANDS,
  CalendarFeature,
  ForecastOptions,
  MAX_CONTEXT,
  MAX_HORIZON_STEPS,
  NATIVE_HORIZON_STEPS,
  QueryRole,
} from './types';

/** A numeric series resampled onto a regular time grid, ready for Timestral. */
export interface GridSeries {
  name: string;
  start: number;
  step: number;
  values: Array<number | null>;
}

export interface SourceSeries {
  name: string;
  refId?: string;
  frame: DataFrame;
  field: Field;
  timeField: Field;
}

/** Collects every numeric field that has a time field in its frame, optionally filtered by query refId. */
export function collectSeries(
  frames: DataFrame[],
  maxSeries: number,
  include: (refId?: string) => boolean = () => true
): SourceSeries[] {
  const out: SourceSeries[] = [];
  for (const frame of frames) {
    if (!include(frame.refId)) {
      continue;
    }
    const timeField = frame.fields.find((f) => f.type === FieldType.time);
    if (!timeField) {
      continue;
    }
    for (const field of frame.fields) {
      if (field.type !== FieldType.number || out.length >= maxSeries) {
        continue;
      }
      out.push({ name: getFieldDisplayName(field, frame, frames), refId: frame.refId, frame, field, timeField });
    }
  }
  return out;
}

function median(xs: number[]): number {
  const s = [...xs].sort((a, b) => a - b);
  return s[Math.floor(s.length / 2)];
}

/**
 * Resamples a series onto a regular grid. The step is the median spacing between samples, so
 * occasional scrape gaps become nulls (Timestral treats them as missing) instead of skewing time.
 */
export function toGrid(times: number[], values: Array<number | null | undefined>, name = ''): GridSeries | undefined {
  const pts: Array<[number, number | null]> = [];
  for (let i = 0; i < times.length; i++) {
    const v = values[i];
    pts.push([times[i], v == null || !Number.isFinite(v) ? null : v]);
  }
  pts.sort((a, b) => a[0] - b[0]);
  if (pts.length < 2) {
    return undefined;
  }
  const diffs: number[] = [];
  for (let i = 1; i < pts.length; i++) {
    const d = pts[i][0] - pts[i - 1][0];
    if (d > 0) {
      diffs.push(d);
    }
  }
  if (!diffs.length) {
    return undefined;
  }
  const step = median(diffs);
  // Keep only the most recent MAX_CONTEXT cells before allocating: irregular or bursty data can
  // have a tiny median step over a long span.
  const lastT = pts[pts.length - 1][0];
  const kept = pts.filter(([t]) => t >= lastT - (MAX_CONTEXT - 1) * step);
  const start = kept[0][0];
  const n = Math.round((lastT - start) / step) + 1;
  const grid: Array<number | null> = new Array(n).fill(null);
  for (const [t, v] of kept) {
    if (v !== null) {
      grid[Math.round((t - start) / step)] = v;
    }
  }
  let trimmed = grid;
  let offset = 0;
  const firstValid = trimmed.findIndex((v) => v !== null);
  if (firstValid < 0) {
    return undefined;
  }
  trimmed = trimmed.slice(firstValid);
  offset += firstValid;
  // Forecast from the last observed point, not from trailing nulls.
  let last = trimmed.length - 1;
  while (last > 0 && trimmed[last] === null) {
    last--;
  }
  trimmed = trimmed.slice(0, last + 1);
  if (trimmed.filter((v) => v !== null).length < 2) {
    return undefined;
  }
  return { name, start: start + offset * step, step, values: trimmed };
}

export function sourceToGrid(s: SourceSeries): GridSeries | undefined {
  return toGrid(s.timeField.values as number[], s.field.values as Array<number | null>, s.name);
}

/**
 * Samples a series onto `length` cells of a grid (start, step); several points falling into one
 * cell are averaged, points outside are dropped, empty cells are null.
 */
export function alignToGrid(
  g: Pick<GridSeries, 'start' | 'step'>,
  length: number,
  times: number[],
  values: Array<number | null | undefined>
): Array<number | null> {
  const sum = new Array(length).fill(0);
  const count = new Array(length).fill(0);
  for (let i = 0; i < times.length; i++) {
    const v = values[i];
    const idx = Math.round((times[i] - g.start) / g.step);
    if (idx >= 0 && idx < length && v != null && Number.isFinite(v)) {
      sum[idx] += v;
      count[idx]++;
    }
  }
  return sum.map((s, i) => (count[i] ? s / count[i] : null));
}

function blockMean(values: Array<number | null>, factor: number): Array<number | null> {
  const out: Array<number | null> = [];
  for (let end = values.length; end > 0; end -= factor) {
    const block = values.slice(Math.max(0, end - factor), end).filter((v): v is number => v !== null);
    out.unshift(block.length ? block.reduce((a, b) => a + b, 0) / block.length : null);
  }
  return out;
}

/**
 * Averages blocks of `factor` points (aligned to the last point) so a long horizon fits in a single
 * Timestral block instead of being forecast block-by-block.
 */
export function downsample(g: GridSeries, factor: number): GridSeries {
  if (factor <= 1) {
    return g;
  }
  const values = blockMean(g.values, factor);
  const step = g.step * factor;
  const last = g.start + (g.values.length - 1) * g.step;
  return { ...g, step, start: last - (values.length - 1) * step, values };
}

/**
 * Horizon in milliseconds: an explicit Grafana duration, or for "auto" the future part of the
 * range when it reaches past now (e.g. now-7d..now+1d), else 25% of the visible range.
 */
export function horizonMs(horizon: string, range: TimeRange, now = Date.now()): number {
  if (!horizon || horizon === 'auto') {
    const future = range.to.valueOf() - now;
    if (future > 60_000) {
      return future;
    }
    return Math.max((range.to.valueOf() - range.from.valueOf()) * 0.25, 60_000);
  }
  return rangeUtil.intervalToMs(horizon);
}

export function horizonSteps(ms: number, step: number): number {
  return Math.max(1, Math.min(MAX_HORIZON_STEPS, Math.round(ms / step)));
}

/** Grid and number of steps to forecast `ms` ahead, coarsening the grid when the horizon is very long. */
export function prepareGrid(g: GridSeries, ms: number): { grid: GridSeries; steps: number } {
  const factor = Math.ceil(Math.round(ms / g.step) / NATIVE_HORIZON_STEPS);
  const grid = downsample(g, factor);
  return { grid, steps: horizonSteps(ms, grid.step) };
}

const WEEKDAYS: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };
const zoneFormats = new Map<string, Intl.DateTimeFormat>();

/** Hour of day and day of week of a timestamp in a Grafana time zone ("browser", "utc" or IANA). */
export function zonedHourAndDay(ms: number, timeZone?: string): { hour: number; day: number } {
  const d = new Date(ms);
  if (!timeZone || timeZone === 'browser') {
    return { hour: d.getHours(), day: d.getDay() };
  }
  if (timeZone === 'utc') {
    return { hour: d.getUTCHours(), day: d.getUTCDay() };
  }
  let fmt = zoneFormats.get(timeZone);
  if (!fmt) {
    try {
      fmt = new Intl.DateTimeFormat('en-US', { timeZone, hour: 'numeric', hourCycle: 'h23', weekday: 'short' });
    } catch {
      return zonedHourAndDay(ms, 'browser'); // unknown zone: fall back to the viewer's
    }
    zoneFormats.set(timeZone, fmt);
  }
  const parts = fmt.formatToParts(d);
  return {
    hour: Number(parts.find((p) => p.type === 'hour')?.value ?? 0),
    day: WEEKDAYS[parts.find((p) => p.type === 'weekday')?.value ?? 'Sun'] ?? 0,
  };
}

/** Calendar covariate values for `length` grid cells; hour/day are categorical codes. */
export function calendarRow(
  feature: CalendarFeature,
  g: Pick<GridSeries, 'start' | 'step'>,
  length: number,
  timeZone?: string
): number[] {
  return Array.from({ length }, (_, i) => {
    const { hour, day } = zonedHourAndDay(g.start + i * g.step, timeZone);
    switch (feature) {
      case 'hour_of_day':
        return hour;
      case 'day_of_week':
        return day;
      case 'weekend':
        return day === 0 || day === 6 ? 1 : 0;
    }
  });
}

const CATEGORICAL_CALENDAR: Record<CalendarFeature, boolean> = {
  hour_of_day: true,
  day_of_week: true,
  weekend: false,
};

/** One forecasted target: where its forecast starts and how it was produced. */
export interface ForecastTarget {
  name: string;
  /** Grid the forecast is on (possibly coarser than the data). */
  start: number;
  step: number;
  /** Index in the grid of the last context point; the forecast starts right after it. */
  contextEnd: number;
  lastValue: number | null;
  /** Observed values after the context end (backtest only). */
  actual?: Array<number | null>;
  series: number;
  variate: number;
}

/** A request to the backend with the targets it produces. */
export interface ForecastJob {
  steps: number;
  series: SeriesInput[];
  targets: ForecastTarget[];
}

export interface JobPlan {
  jobs: ForecastJob[];
  covariates: number;
  /** Why nothing can be forecast, when jobs is empty. */
  empty?: 'no-targets' | 'too-short' | 'backtest-too-short';
}

const roleOf = (roles: Record<string, QueryRole>, refId?: string): QueryRole => (refId && roles[refId]) || 'target';

/**
 * Plans the backend requests for the panel data: which series are forecast, with which
 * covariates, on which grid and how many steps. Targets of one request share the number of
 * steps; in joint mode all targets are variates of a single series on the first target's grid.
 */
export function planJobs(
  frames: DataFrame[],
  options: Pick<ForecastOptions, 'roles' | 'joint' | 'calendar' | 'maxSeries' | 'backtest'>,
  ms: number,
  timeZone?: string
): JobPlan {
  const { roles, joint, calendar, maxSeries, backtest } = options;
  const targets = collectSeries(frames, maxSeries, (r) => roleOf(roles, r) === 'target');
  const covs = collectSeries(frames, 64, (r) => {
    const role = roleOf(roles, r);
    return role !== 'target' && role !== 'ignore';
  });
  const byRole = (role: QueryRole) => covs.filter((c) => roleOf(roles, c.refId) === role);
  const past = byRole('past');
  const known = byRole('known');
  const catPast = byRole('categorical_past');
  const catKnown = byRole('categorical_known');
  const covariates = covs.length + calendar.length;

  const grids = targets.map(sourceToGrid).filter((g): g is GridSeries => !!g);
  if (!grids.length) {
    return { jobs: [], covariates, empty: targets.length ? 'too-short' : 'no-targets' };
  }

  // One entry per Timestral series: its grid (already coarsened) and target rows.
  type Unit = { grid: GridSeries; steps: number; targets: GridSeries[] };
  let units: Unit[];
  if (joint) {
    const { grid, steps } = prepareGrid(grids[0], ms);
    const aligned = grids.map((g, i) => ({
      ...g,
      start: grid.start,
      step: grid.step,
      values: i === 0 ? grid.values : alignToGrid(grid, grid.values.length, gridTimes(g), g.values),
    }));
    units = [{ grid, steps, targets: aligned }];
  } else {
    units = grids.map((g) => {
      const { grid, steps } = prepareGrid(g, ms);
      return { grid, steps, targets: [grid] };
    });
  }

  const jobs = new Map<number, ForecastJob>();
  for (const u of units) {
    const n = u.grid.values.length;
    // In a backtest the context stops `steps` before the end; what follows is the actual.
    const ctxLen = backtest ? n - u.steps : n;
    if (ctxLen < 2) {
      continue;
    }
    const align = (s: SourceSeries, length: number) =>
      alignToGrid(u.grid, length, s.timeField.values as number[], s.field.values as Array<number | null>);
    const knownLen = ctxLen + u.steps;
    const input: SeriesInput = {
      targets: u.targets.map((t) => t.values.slice(0, ctxLen)),
      past_covariates: past.map((c) => align(c, ctxLen)),
      known_covariates: [
        ...known.map((c) => align(c, knownLen)),
        ...calendar.filter((f) => !CATEGORICAL_CALENDAR[f]).map((f) => calendarRow(f, u.grid, knownLen, timeZone)),
      ],
      categorical_past_covariates: catPast.map((c) => align(c, ctxLen)),
      categorical_known_covariates: [
        ...catKnown.map((c) => align(c, knownLen)),
        ...calendar.filter((f) => CATEGORICAL_CALENDAR[f]).map((f) => calendarRow(f, u.grid, knownLen, timeZone)),
      ],
    };
    const job = jobs.get(u.steps) ?? { steps: u.steps, series: [], targets: [] };
    const seriesIdx = job.series.length;
    job.series.push(input);
    u.targets.forEach((t, variate) => {
      const context = t.values.slice(0, ctxLen);
      job.targets.push({
        name: t.name,
        start: u.grid.start,
        step: u.grid.step,
        contextEnd: ctxLen - 1,
        lastValue: lastNonNull(context),
        actual: backtest ? t.values.slice(ctxLen) : undefined,
        series: seriesIdx,
        variate,
      });
    });
    jobs.set(u.steps, job);
  }
  return { jobs: [...jobs.values()], covariates, empty: jobs.size ? undefined : 'backtest-too-short' };
}

function gridTimes(g: GridSeries): number[] {
  return g.values.map((_, i) => g.start + i * g.step);
}

function lastNonNull(values: Array<number | null>): number | null {
  for (let i = values.length - 1; i >= 0; i--) {
    if (values[i] !== null) {
      return values[i];
    }
  }
  return null;
}

export const forecastNames = (name: string) => ({
  median: `${name} · forecast`,
  band: (lo: number, hi: number) => ({
    lower: `${name} · p${Math.round(lo * 100)}`,
    upper: `${name} · p${Math.round(hi * 100)}`,
  }),
  anomalies: `${name} · anomalies`,
});

/** Quantile levels to request for a band option (always including the median). */
export function requestedQuantiles(band: ForecastOptions['band']): number[] {
  const levels = new Set<number>([0.5]);
  if (band !== 'none') {
    for (const [lo, hi] of BANDS[band]) {
      levels.add(lo);
      levels.add(hi);
    }
  }
  return [...levels].sort((a, b) => a - b);
}

const qkey = (q: number) => String(q);

/** Builds the forecast frame of a target (time, median, band quantiles) and, in backtests, its anomalies. */
export function buildForecastFrames(
  target: ForecastTarget,
  res: ForecastResponse,
  bands: Array<[number, number]>
): DataFrame[] {
  const fc = res.forecasts[target.series][target.variate];
  const lastT = target.start + target.contextEnd * target.step;
  const lastV = target.lastValue;
  // Prepend the last observed point so the forecast visibly continues the series.
  const times = [lastT, ...fc.median.map((_, k) => lastT + (k + 1) * target.step)];
  const names = forecastNames(target.name);
  const fields: Array<Partial<Field>> = [
    { name: 'Time', type: FieldType.time, values: times },
    numberField(names.median, [lastV, ...fc.median]),
  ];
  for (const [lo, hi] of bands) {
    const l = fc.quantiles[qkey(lo)];
    const h = fc.quantiles[qkey(hi)];
    if (l && h) {
      const n = names.band(lo, hi);
      fields.push(numberField(n.lower, [lastV, ...l]), numberField(n.upper, [lastV, ...h]));
    }
  }
  const frames = [createDataFrame({ name: names.median, refId: `forecast-${target.name}`, fields })];

  if (target.actual && bands.length) {
    // Backtest: actual points outside the widest band are anomalies.
    const [lo, hi] = bands[0];
    const l = fc.quantiles[qkey(lo)];
    const h = fc.quantiles[qkey(hi)];
    const vals = target.actual
      .slice(0, fc.median.length)
      .map((a, k) => (a !== null && l && h && (a < l[k] || a > h[k]) ? a : null));
    frames.push(
      createDataFrame({
        name: names.anomalies,
        refId: `anomalies-${target.name}`,
        fields: [
          { name: 'Time', type: FieldType.time, values: vals.map((_, k) => lastT + (k + 1) * target.step) },
          numberField(names.anomalies, vals),
        ],
      })
    );
  }
  return frames;
}

function numberField(name: string, values: Array<number | null>): Partial<Field> {
  return { name, type: FieldType.number, values, config: { displayName: name } };
}

export interface BacktestScore {
  name: string;
  /** Share of actual points inside the band. */
  coverage?: number;
  mape?: number;
  mae: number;
  anomalies: number;
  points: number;
}

/** Accuracy of a backtest forecast against what happened. */
export function scoreBacktest(
  target: ForecastTarget,
  res: ForecastResponse,
  band?: [number, number]
): BacktestScore | undefined {
  if (!target.actual) {
    return undefined;
  }
  const fc = res.forecasts[target.series][target.variate];
  const l = band && fc.quantiles[qkey(band[0])];
  const h = band && fc.quantiles[qkey(band[1])];
  let n = 0;
  let abs = 0;
  let pct = 0;
  let pctN = 0;
  let inside = 0;
  target.actual.forEach((a, k) => {
    if (a === null || k >= fc.median.length) {
      return;
    }
    n++;
    abs += Math.abs(a - fc.median[k]);
    if (a !== 0) {
      pct += Math.abs((a - fc.median[k]) / a);
      pctN++;
    }
    if (l && h && a >= l[k] && a <= h[k]) {
      inside++;
    }
  });
  if (!n) {
    return undefined;
  }
  return {
    name: target.name,
    mae: abs / n,
    mape: pctN ? pct / pctN : undefined,
    coverage: l && h ? inside / n : undefined,
    anomalies: l && h ? n - inside : 0,
    points: n,
  };
}

export interface ThresholdCrossing {
  name: string;
  /** First time the median crosses the threshold. */
  median?: number;
  /** First time the band edge on the threshold side crosses it (earliest plausible). */
  early?: number;
  earlyLevel?: number;
}

/** When a target's forecast is expected to cross a threshold. */
export function thresholdCrossing(
  target: ForecastTarget,
  res: ForecastResponse,
  threshold: number,
  direction: 'above' | 'below',
  band?: [number, number]
): ThresholdCrossing {
  const fc = res.forecasts[target.series][target.variate];
  const t0 = target.start + target.contextEnd * target.step;
  const crosses = (v: number) => (direction === 'above' ? v >= threshold : v <= threshold);
  const first = (vals?: number[]) => {
    const i = vals ? vals.findIndex(crosses) : -1;
    return i >= 0 ? t0 + (i + 1) * target.step : undefined;
  };
  const edgeLevel = band ? (direction === 'above' ? band[1] : band[0]) : undefined;
  return {
    name: target.name,
    median: first(fc.median),
    early: edgeLevel !== undefined ? first(fc.quantiles[qkey(edgeLevel)]) : undefined,
    earlyLevel: edgeLevel,
  };
}

/**
 * Grafana's time series tooltip snaps a series whose value is missing (undefined after the
 * join) to its nearest point at any distance, but limits the snap to a few pixels for explicit
 * nulls. Give every frame explicit nulls at the other frames' timestamps that fall outside its
 * own range, so history does not show forecast values in the tooltip (and vice versa). Gaps
 * inside a frame are left alone so lines are not broken.
 */
export function padOutsideWithNulls(frames: DataFrame[]): DataFrame[] {
  const all = new Set<number>();
  for (const f of frames) {
    const t = f.fields.find((x) => x.type === FieldType.time);
    t?.values.forEach((v: number) => all.add(v));
  }
  const times = [...all].sort((a, b) => a - b);
  return frames.map((frame) => {
    const ti = frame.fields.findIndex((x) => x.type === FieldType.time);
    if (ti < 0 || frame.length === 0) {
      return frame;
    }
    const own = frame.fields[ti].values as number[];
    const first = own[0];
    const last = own[own.length - 1];
    const before = times.filter((t) => t < first);
    const after = times.filter((t) => t > last);
    if (!before.length && !after.length) {
      return frame;
    }
    const nulls = (n: number) => new Array(n).fill(null);
    return {
      ...frame,
      length: before.length + own.length + after.length,
      fields: frame.fields.map((f, i) => ({
        ...f,
        values:
          i === ti ? [...before, ...own, ...after] : [...nulls(before.length), ...f.values, ...nulls(after.length)],
      })),
    };
  });
}
