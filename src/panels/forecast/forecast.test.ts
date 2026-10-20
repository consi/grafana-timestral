import { createDataFrame, dateTime, FieldType } from '@grafana/data';
import { ForecastResponse } from '../../api';
import {
  alignToGrid,
  buildForecastFrames,
  calendarRow,
  downsample,
  horizonMs,
  horizonSteps,
  padOutsideWithNulls,
  planJobs,
  prepareGrid,
  requestedQuantiles,
  scoreBacktest,
  thresholdCrossing,
  toGrid,
} from './forecast';
import { DEFAULT_OPTIONS, withDefaults } from './types';

const frame = (refId: string, name: string, times: number[], values: number[]) =>
  createDataFrame({
    refId,
    fields: [
      { name: 'time', type: FieldType.time, values: times },
      { name, type: FieldType.number, values },
    ],
  });

const range = (n: number, step = 60_000) => Array.from({ length: n }, (_, i) => i * step);

describe('toGrid', () => {
  it('fills gaps with nulls on the median step', () => {
    const g = toGrid([0, 10, 20, 40, 50], [1, 2, 3, 5, 6])!;
    expect(g.step).toBe(10);
    expect(g.values).toEqual([1, 2, 3, null, 5, 6]);
  });

  it('trims leading and trailing missing values', () => {
    const g = toGrid([0, 10, 20, 30, 40], [null, 2, NaN, 4, null])!;
    expect(g.start).toBe(10);
    expect(g.values).toEqual([2, null, 4]);
  });

  it('clamps bursty data to the context before allocating', () => {
    const times = [0, ...Array.from({ length: 100 }, (_, i) => 30 * 86400_000 + i)];
    const g = toGrid(
      times,
      times.map((_, i) => i)
    )!;
    expect(g.step).toBe(1);
    expect(g.values).toHaveLength(100);
  });

  it('rejects series with fewer than two points', () => {
    expect(toGrid([0], [1])).toBeUndefined();
  });
});

describe('horizon', () => {
  const r = { from: dateTime(0), to: dateTime(4 * 3600_000), raw: { from: 'now-4h', to: 'now' } };
  it('auto uses a quarter of the range', () => expect(horizonMs('auto', r, 4 * 3600_000)).toBe(3600_000));
  it('auto fills the future part of the range', () => expect(horizonMs('auto', r, 3 * 3600_000)).toBe(3600_000 * 1));
  it('auto fills a long future range', () => expect(horizonMs('auto', r, 3600_000)).toBe(3 * 3600_000));
  it('parses durations', () => expect(horizonMs('1d', r)).toBe(86400_000));
  it('clamps steps', () => expect(horizonSteps(1e12, 1000)).toBe(16384));
});

describe('grids', () => {
  it('averages points falling into one cell', () => {
    expect(alignToGrid({ start: 100, step: 10 }, 4, [90, 100, 121, 124, 130], [9, 5, 6, 8, 7])).toEqual([
      5,
      null,
      7,
      7,
    ]);
  });

  it('coarsens the grid so long horizons fit one block', () => {
    const g = { name: 'x', start: 0, step: 60_000, values: [1, 2, 3, 4, 5, null, 7] };
    const { grid, steps } = prepareGrid(g, 2000 * 60_000);
    expect(grid.step).toBe(120_000);
    expect(grid.values).toEqual([1, 2.5, 4.5, 7]);
    expect(steps).toBe(1000);
    expect(downsample(g, 1)).toBe(g);
  });

  it('derives calendar features', () => {
    const monday = Date.UTC(2026, 9, 19, 23, 0);
    expect(calendarRow('hour_of_day', { start: monday, step: 3600_000 }, 2, 'utc')).toEqual([23, 0]);
    expect(calendarRow('hour_of_day', { start: monday, step: 3600_000 }, 2, 'Asia/Tokyo')).toEqual([8, 9]);
    expect(calendarRow('day_of_week', { start: monday, step: 3600_000 }, 1, 'Asia/Tokyo')).toEqual([2]);
    expect(calendarRow('day_of_week', { start: monday, step: 3600_000 }, 2, 'utc')).toEqual([1, 2]);
    expect(calendarRow('weekend', { start: Date.UTC(2026, 9, 24), step: 86400_000 }, 3, 'utc')).toEqual([1, 1, 0]);
  });
});

describe('planJobs', () => {
  const t = range(100);
  const frames = [
    frame(
      'A',
      'cpu',
      t,
      t.map((_, i) => i)
    ),
    frame(
      'B',
      'bytes',
      t,
      t.map((_, i) => 2 * i)
    ),
    frame(
      'C',
      'mem',
      t,
      t.map((_, i) => 3 * i)
    ),
  ];
  const opts = { ...DEFAULT_OPTIONS, roles: {}, calendar: [] };

  it('forecasts every series by default', () => {
    const plan = planJobs(frames, opts, 10 * 60_000, 'utc');
    expect(plan.jobs).toHaveLength(1);
    expect(plan.jobs[0].steps).toBe(10);
    expect(plan.jobs[0].series).toHaveLength(3);
    expect(plan.jobs[0].targets.map((x) => x.name)).toEqual(['cpu', 'bytes', 'mem']);
  });

  it('routes queries by role and adds calendar features', () => {
    const plan = planJobs(
      frames,
      { ...opts, roles: { B: 'past', C: 'known' }, calendar: ['hour_of_day', 'weekend'] },
      10 * 60_000,
      'utc'
    );
    const s = plan.jobs[0].series[0];
    expect(plan.jobs[0].targets.map((x) => x.name)).toEqual(['cpu']);
    expect(plan.covariates).toBe(4);
    expect(s.past_covariates).toHaveLength(1);
    expect(s.past_covariates![0]).toHaveLength(100);
    // Known covariates span context + horizon; C has no future values, so they are missing.
    expect(s.known_covariates).toHaveLength(2);
    expect(s.known_covariates![0]).toHaveLength(110);
    expect(s.known_covariates![0][105]).toBeNull();
    expect(s.categorical_known_covariates).toHaveLength(1);
  });

  it('forecasts jointly as variates of one series', () => {
    const plan = planJobs(frames, { ...opts, joint: true }, 10 * 60_000, 'utc');
    expect(plan.jobs[0].series).toHaveLength(1);
    expect(plan.jobs[0].series[0].targets).toHaveLength(3);
    expect(plan.jobs[0].targets.map((x) => x.variate)).toEqual([0, 1, 2]);
  });

  it('explains why nothing can be forecast', () => {
    expect(planJobs(frames, { ...opts, roles: { A: 'past', B: 'past', C: 'past' } }, 60_000, 'utc').empty).toBe(
      'no-targets'
    );
    expect(planJobs([frame('A', 'x', [0], [1])], opts, 60_000, 'utc').empty).toBe('too-short');
    expect(planJobs(frames, { ...opts, backtest: true }, 200 * 60_000, 'utc').empty).toBe('backtest-too-short');
  });

  it('holds out the horizon in a backtest', () => {
    const plan = planJobs(frames, { ...opts, backtest: true }, 10 * 60_000, 'utc');
    const target = plan.jobs[0].targets[0];
    expect(plan.jobs[0].series[0].targets[0]).toHaveLength(90);
    expect(target.contextEnd).toBe(89);
    expect(target.actual).toEqual(t.slice(90).map((_, i) => 90 + i));
  });
});

const res: ForecastResponse = {
  model: 'm',
  quantile_levels: [0.1, 0.5, 0.9],
  forecasts: [[{ median: [4, 5, 6], quantiles: { '0.1': [3, 4, 5], '0.5': [4, 5, 6], '0.9': [5, 6, 7] } }]],
};
const target = { name: 'cpu', start: 0, step: 10, contextEnd: 2, lastValue: 3, series: 0, variate: 0 };

describe('results', () => {
  it('requests the band levels and the median', () => {
    expect(requestedQuantiles('80')).toEqual([0.1, 0.5, 0.9]);
    expect(requestedQuantiles('fan')).toEqual([0.01, 0.05, 0.1, 0.25, 0.5, 0.75, 0.9, 0.95, 0.99]);
  });

  it('builds frames that continue from the last observed point', () => {
    const [f] = buildForecastFrames(target, res, [[0.1, 0.9]]);
    expect(f.fields.map((x) => x.name)).toEqual(['Time', 'cpu · forecast', 'cpu · p10', 'cpu · p90']);
    expect(f.fields[0].values).toEqual([20, 30, 40, 50]);
    expect(f.fields[1].values).toEqual([3, 4, 5, 6]);
  });

  it('flags backtest anomalies and scores accuracy', () => {
    const bt = { ...target, actual: [4, 9, 6] };
    const frames = buildForecastFrames(bt, res, [[0.1, 0.9]]);
    expect(frames[1].fields[1].values).toEqual([null, 9, null]);
    const score = scoreBacktest(bt, res, [0.1, 0.9])!;
    expect(score.coverage).toBeCloseTo(2 / 3);
    expect(score.anomalies).toBe(1);
    expect(score.mae).toBeCloseTo(4 / 3);
  });

  it('finds threshold crossings', () => {
    const c = thresholdCrossing(target, res, 5.5, 'above', [0.1, 0.9]);
    expect(c.median).toBe(50);
    expect(c.early).toBe(40);
    expect(thresholdCrossing(target, res, 100, 'above').median).toBeUndefined();
  });
});

describe('padOutsideWithNulls', () => {
  it('adds explicit nulls only outside each frame range', () => {
    const a = frame('A', 'a', [0, 10, 30], [1, 2, 3]);
    const b = frame('B', 'b', [30, 40], [5, 6]);
    const [pa, pb] = padOutsideWithNulls([a, b]);
    expect(pa.fields[0].values).toEqual([0, 10, 30, 40]);
    expect(pa.fields[1].values).toEqual([1, 2, 3, null]);
    expect(pb.fields[1].values).toEqual([null, null, 5, 6]);
  });
});

describe('withDefaults', () => {
  it('fills missing options and keeps nested identities', () => {
    const roles = { B: 'past' as const };
    const o = withDefaults({ band: '90', roles });
    expect(o.band).toBe('90');
    expect(o.horizon).toBe('auto');
    expect(o.roles).toBe(roles);
  });
});
