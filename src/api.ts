import { DataSourceWithBackend, getDataSourceSrv } from '@grafana/runtime';
import { DS_TYPE } from './constants';

/** One series: target variates (forecast jointly) plus covariates; null marks a missing value. */
export interface SeriesInput {
  targets: Array<Array<number | null>>;
  /** Known over the context only (same length as the targets). */
  past_covariates?: Array<Array<number | null>>;
  /** Known over the context and the horizon (targets length + prediction_length). */
  known_covariates?: Array<Array<number | null>>;
  categorical_past_covariates?: Array<Array<number | null>>;
  categorical_known_covariates?: Array<Array<number | null>>;
}

export interface ForecastOptionsPayload {
  max_context?: number;
  per_shot_horizon_patches?: number;
  quantile_real_cap_k?: number;
  symmetric_averaging?: boolean;
  clip_nonnegative?: boolean;
}

export interface ForecastRequest {
  prediction_length: number;
  series: SeriesInput[];
  quantiles?: number[];
  options?: ForecastOptionsPayload;
}

export interface VariateForecast {
  median: number[];
  quantiles: Record<string, number[]>;
}

export interface ForecastResponse {
  model: string;
  quantile_levels: number[];
  /** Per series, one forecast per target variate. */
  forecasts: VariateForecast[][];
}

export interface StatusResponse {
  model: string;
  loaded: boolean;
  error?: string;
}

/** Thrown when no Timestral data source exists yet; it runs the model for the whole app. */
export class NoDatasourceError extends Error {
  constructor() {
    super('Create the Timestral data source first (Administration → Plugins → Timestral)');
  }
}

async function timestralDatasource(): Promise<DataSourceWithBackend> {
  const settings = getDataSourceSrv().getList({ type: DS_TYPE })[0];
  if (!settings) {
    throw new NoDatasourceError();
  }
  return (await getDataSourceSrv().get(settings.uid)) as DataSourceWithBackend;
}

export async function postForecast(body: ForecastRequest, requestId?: string): Promise<ForecastResponse> {
  const ds = await timestralDatasource();
  return ds.postResource<ForecastResponse>('forecast', body, { requestId, showErrorAlert: false });
}

export async function getStatus(): Promise<StatusResponse> {
  const ds = await timestralDatasource();
  return ds.getResource<StatusResponse>('status', undefined, { showErrorAlert: false });
}
