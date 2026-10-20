import { DataSourceJsonData } from '@grafana/data';
import { DataQuery, DataSourceRef } from '@grafana/schema';
import { BandOption, CalendarFeature, QueryRole, ThresholdDirection } from '../shared/options';

/** A query of another data source and how its series are used. */
export interface ForecastInput {
  refId: string;
  datasource?: DataSourceRef;
  query?: DataQuery;
  role: QueryRole;
}

export interface TimestralQuery extends DataQuery {
  inputs: ForecastInput[];
  /** Grafana duration ("1d") or "auto" (see HORIZON_HELP). */
  horizon: string;
  band: BandOption;
  /** "forecast": median + quantile series; "eta": seconds until the threshold is crossed. */
  output: 'forecast' | 'eta';
  includeHistory: boolean;
  threshold?: number;
  thresholdDirection: ThresholdDirection;
  calendar: CalendarFeature[];
  /** IANA time zone for calendar features; empty = the dashboard's (UTC in alert rules). */
  timezone?: string;
  joint: boolean;
  maxSeries: number;
  symmetricAveraging: boolean;
  clipNonnegative: boolean;
  maxContext: number;
  blockPatches: number;
  quantileCapK: number;
}

export const DEFAULT_QUERY: Partial<TimestralQuery> = {
  inputs: [],
  horizon: 'auto',
  band: '80',
  output: 'forecast',
  includeHistory: true,
  thresholdDirection: 'above',
  calendar: [],
  joint: false,
  maxSeries: 20,
  symmetricAveraging: true,
  clipNonnegative: true,
  maxContext: 0,
  blockPatches: 32,
  quantileCapK: 10000,
};

export interface TimestralOptions extends DataSourceJsonData {
  /** URL of Grafana's API as seen from the Grafana server; default: Grafana's own URL. */
  grafanaUrl?: string;
}

export interface TimestralSecureOptions {
  serviceAccountToken?: string;
}
