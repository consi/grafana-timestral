/** Forecast options shared by the panel and the data source query editor. */

export type BandOption = 'none' | '50' | '80' | '90' | '98' | 'fan';

/** How the series of one query are used. */
export type QueryRole = 'target' | 'past' | 'known' | 'categorical_past' | 'categorical_known' | 'ignore';

export type CalendarFeature = 'hour_of_day' | 'day_of_week' | 'weekend';

export type ThresholdDirection = 'above' | 'below';

/** Bands as [lower, upper] quantile levels, widest first so the narrowest is drawn on top. */
export const BANDS: Record<Exclude<BandOption, 'none'>, Array<[number, number]>> = {
  '50': [[0.25, 0.75]],
  '80': [[0.1, 0.9]],
  '90': [[0.05, 0.95]],
  '98': [[0.01, 0.99]],
  fan: [
    [0.01, 0.99],
    [0.05, 0.95],
    [0.1, 0.9],
    [0.25, 0.75],
  ],
};

export const BAND_OPTIONS: Array<{ label: string; value: BandOption; description?: string }> = [
  { label: '50% band', value: '50' },
  { label: '80% band', value: '80' },
  { label: '90% band', value: '90' },
  { label: '98% band', value: '98' },
  { label: 'Quantile fan', value: 'fan', description: '50 / 80 / 90 / 98% bands' },
  { label: 'No band', value: 'none' },
];

export const ROLE_OPTIONS: Array<{ label: string; value: QueryRole; description: string }> = [
  { label: 'Forecast', value: 'target', description: 'Forecast every series of this query' },
  { label: 'Past covariate', value: 'past', description: 'An input known over the history only' },
  { label: 'Known-future covariate', value: 'known', description: 'An input also known over the horizon' },
  { label: 'Categorical past covariate', value: 'categorical_past', description: 'Category codes over the history' },
  {
    label: 'Categorical known-future covariate',
    value: 'categorical_known',
    description: 'Category codes over history and horizon',
  },
  { label: 'Ignore', value: 'ignore', description: 'Keep the query but do not use it' },
];

export const CALENDAR_OPTIONS: Array<{ label: string; value: CalendarFeature }> = [
  { label: 'Hour of day', value: 'hour_of_day' },
  { label: 'Day of week', value: 'day_of_week' },
  { label: 'Weekend', value: 'weekend' },
];

export const DIRECTION_OPTIONS: Array<{ label: string; value: ThresholdDirection }> = [
  { label: 'Above', value: 'above' },
  { label: 'Below', value: 'below' },
];

export const HORIZON_PRESETS = ['auto', '1h', '6h', '12h', '1d', '3d', '7d', '30d'];

export const HORIZON_HELP =
  'How far ahead to forecast, e.g. "6h" or "3d". "auto" fills the part of the time range after now, or forecasts 25% of the range when it ends at now.';
