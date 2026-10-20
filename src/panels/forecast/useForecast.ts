import { useEffect, useMemo, useState } from 'react';
import { DataFrame, TimeRange } from '@grafana/data';
import { ForecastRequest, postForecast } from '../../api';
import {
  BacktestScore,
  buildForecastFrames,
  horizonMs,
  planJobs,
  requestedQuantiles,
  scoreBacktest,
  thresholdCrossing,
  ThresholdCrossing,
} from './forecast';
import { BANDS, ForecastOptions } from './types';

export interface ForecastState {
  frames: DataFrame[];
  horizonEnd?: number;
  loading: boolean;
  error?: string;
  info?: string;
  scores: BacktestScore[];
  crossings: ThresholdCrossing[];
  /** When the forecast was computed (threshold ETAs are relative to it). */
  computedAt?: number;
}

const idle: ForecastState = { frames: [], loading: false, scores: [], crossings: [] };

const EMPTY_PLAN_MESSAGES = {
  'no-targets': 'No series to forecast: every query is an input or ignored',
  'too-short': 'Not enough history to forecast (at least 2 points are needed)',
  'backtest-too-short': 'The backtest needs more history than the forecast horizon',
};

export function useForecast(
  panelId: number | string,
  series: DataFrame[],
  timeRange: TimeRange,
  timeZone: string | undefined,
  options: ForecastOptions
): ForecastState {
  const from = timeRange.from.valueOf();
  const to = timeRange.to.valueOf();
  const ms = useMemo(() => {
    try {
      return horizonMs(options.horizon, timeRange);
    } catch {
      return undefined;
    }
    // timeRange only matters for "auto" horizon; depend on its numeric bounds to avoid re-firing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [options.horizon, from, to]);

  const { enabled, roles, joint, calendar, maxSeries, backtest, band } = options;
  const plan = useMemo(
    () =>
      enabled && ms !== undefined
        ? planJobs(series, { roles, joint, calendar, maxSeries, backtest }, ms, timeZone)
        : undefined,
    [enabled, ms, series, roles, joint, calendar, maxSeries, backtest, timeZone]
  );

  const { symmetricAveraging, clipNonnegative, maxContext, blockPatches, quantileCapK, threshold, thresholdDirection } =
    options;
  // Each distinct request gets a new object identity; results are tagged with the request they answer.
  const request = useMemo(
    () =>
      plan && plan.jobs.length
        ? {
            plan,
            band,
            threshold,
            thresholdDirection,
            options: {
              symmetric_averaging: symmetricAveraging,
              clip_nonnegative: clipNonnegative,
              max_context: maxContext || undefined,
              per_shot_horizon_patches: blockPatches || undefined,
              quantile_real_cap_k: quantileCapK,
            },
          }
        : undefined,
    [
      plan,
      band,
      threshold,
      thresholdDirection,
      symmetricAveraging,
      clipNonnegative,
      maxContext,
      blockPatches,
      quantileCapK,
    ]
  );
  const [result, setResult] = useState<{ request: typeof request } & Omit<ForecastState, 'loading'>>();

  useEffect(() => {
    if (!request) {
      return;
    }
    let cancelled = false;
    const bands = request.band === 'none' ? [] : BANDS[request.band];
    const widest = bands[0];
    const t0 = performance.now();
    Promise.all(
      request.plan.jobs.map(async (job, i) => {
        const body: ForecastRequest = {
          prediction_length: job.steps,
          series: job.series,
          quantiles: requestedQuantiles(request.band),
          options: request.options,
        };
        return { job, res: await postForecast(body, `timestral-${panelId}-${i}`) };
      })
    )
      .then((results) => {
        if (cancelled) {
          return;
        }
        const frames: DataFrame[] = [];
        const scores: BacktestScore[] = [];
        const crossings: ThresholdCrossing[] = [];
        for (const { job, res } of results) {
          for (const target of job.targets) {
            frames.push(...buildForecastFrames(target, res, bands));
            const score = scoreBacktest(target, res, widest);
            if (score) {
              scores.push(score);
            }
            if (request.threshold != null && !Number.isNaN(request.threshold) && !target.actual) {
              crossings.push(thresholdCrossing(target, res, request.threshold, request.thresholdDirection, widest));
            }
          }
        }
        const horizonEnd = Math.max(...frames.map((f) => f.fields[0].values[f.fields[0].values.length - 1]));
        const nTargets = results.reduce((n, r) => n + r.job.targets.length, 0);
        const cov = request.plan.covariates;
        setResult({
          request,
          frames,
          horizonEnd,
          scores,
          crossings,
          computedAt: Date.now(),
          info: `${nTargets} series${cov ? ` + ${cov} covariates` : ''} · ${Math.round(performance.now() - t0)} ms`,
        });
      })
      .catch((e) => {
        if (cancelled || e?.cancelled) {
          return;
        }
        const msg = e?.data?.message ?? e?.statusText ?? e?.message ?? 'Forecast failed';
        setResult({
          request,
          frames: [],
          scores: [],
          crossings: [],
          error: typeof msg === 'string' ? msg : JSON.stringify(msg),
        });
      });
    return () => {
      cancelled = true;
    };
  }, [request, panelId]);

  if (enabled && ms === undefined) {
    return { ...idle, error: `Invalid horizon "${options.horizon}"` };
  }
  if (!request) {
    if (plan?.empty && series.length) {
      return { ...idle, error: EMPTY_PLAN_MESSAGES[plan.empty] };
    }
    return idle;
  }
  const loading = result?.request !== request;
  // While a new forecast is loading keep showing the previous one to avoid flicker.
  return {
    frames: result?.frames ?? [],
    horizonEnd: result?.horizonEnd,
    info: result?.info,
    scores: result?.scores ?? [],
    crossings: result?.crossings ?? [],
    computedAt: result?.computedAt,
    error: loading ? undefined : result?.error,
    loading,
  };
}
