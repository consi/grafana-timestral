import { BandOption, CalendarFeature, QueryRole, ThresholdDirection } from '../../shared/options';

export {
  BANDS,
  BAND_OPTIONS,
  CALENDAR_OPTIONS,
  DIRECTION_OPTIONS,
  HORIZON_HELP,
  HORIZON_PRESETS,
  ROLE_OPTIONS,
} from '../../shared/options';
export type { BandOption, CalendarFeature, QueryRole, ThresholdDirection } from '../../shared/options';

export interface ForecastOptions {
  enabled: boolean;
  /** Grafana duration ("6h", "2d") or "auto" (see HORIZON_HELP). */
  horizon: string;
  band: BandOption;
  fillOpacity: number;
  maxSeries: number;
  showToolbar: boolean;
  /** Role per query refId; queries not listed are targets. */
  roles: Record<string, QueryRole>;
  /** Forecast all targets together as the variates of one multivariate series. */
  joint: boolean;
  /** Calendar features added as known-future covariates. */
  calendar: CalendarFeature[];
  /** Timestral decoding options. */
  symmetricAveraging: boolean;
  clipNonnegative: boolean;
  /** Context cap in steps; 0 = the model's 32768. */
  maxContext: number;
  /** Patches decoded per block (1-32). */
  blockPatches: number;
  /** Clamp to [min - k*s, max + k*s] of the context; 0 disables. */
  quantileCapK: number;
  /** Optional threshold: report when the forecast is expected to cross it. */
  threshold?: number;
  thresholdDirection: ThresholdDirection;
  /** Forecast the last horizon from the history before it and compare with what happened. */
  backtest: boolean;
}

// Limits of the backend (pkg/series) and the model.
export const MAX_CONTEXT = 32768;
export const MAX_HORIZON_STEPS = 16384;
// Horizons longer than this many steps are forecast block by block; the panel coarsens the
// grid instead so one block covers the horizon.
export const NATIVE_HORIZON_STEPS = 1024;

export const DEFAULT_OPTIONS: ForecastOptions = {
  enabled: true,
  horizon: 'auto',
  band: '80',
  fillOpacity: 20,
  maxSeries: 10,
  showToolbar: true,
  roles: {},
  joint: false,
  calendar: [],
  symmetricAveraging: true,
  clipNonnegative: true,
  maxContext: 0,
  blockPatches: 32,
  quantileCapK: 10000,
  thresholdDirection: 'above',
  backtest: false,
};

/** Fills options missing from a saved panel with defaults (keeps the identity of nested values). */
export function withDefaults(o: Partial<ForecastOptions>): ForecastOptions {
  return { ...DEFAULT_OPTIONS, ...o };
}
