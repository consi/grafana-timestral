import { CoreApp, DataQueryRequest, DataQueryResponse, DataSourceInstanceSettings, ScopedVars } from '@grafana/data';
import { DataSourceWithBackend, getTemplateSrv } from '@grafana/runtime';
import { Observable } from 'rxjs';
import { DEFAULT_QUERY, TimestralOptions, TimestralQuery } from './types';

export class TimestralDataSource extends DataSourceWithBackend<TimestralQuery, TimestralOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<TimestralOptions>) {
    super(instanceSettings);
  }

  getDefaultQuery(_: CoreApp): Partial<TimestralQuery> {
    return DEFAULT_QUERY;
  }

  filterQuery(query: TimestralQuery): boolean {
    return !query.hide && (query.inputs ?? []).some((i) => i.datasource?.uid && i.role !== 'ignore');
  }

  query(request: DataQueryRequest<TimestralQuery>): Observable<DataQueryResponse> {
    // Calendar features follow the dashboard time zone.
    const timezone =
      request.timezone === 'browser' ? Intl.DateTimeFormat().resolvedOptions().timeZone : request.timezone;
    return super.query({ ...request, targets: request.targets.map((t) => ({ ...t, timezone })) });
  }

  applyTemplateVariables(query: TimestralQuery, scopedVars: ScopedVars): TimestralQuery {
    // Interpolate variables inside the embedded input queries (they run server side).
    const srv = getTemplateSrv();
    return {
      ...query,
      horizon: srv.replace(query.horizon ?? '', scopedVars),
      inputs: (query.inputs ?? []).map((input) => ({
        ...input,
        datasource: input.datasource && { ...input.datasource, uid: srv.replace(input.datasource.uid, scopedVars) },
        query: input.query ? JSON.parse(srv.replace(JSON.stringify(input.query), scopedVars, jsonEscape)) : input.query,
      })),
    };
  }
}

/** Formats variable values so they stay valid inside JSON strings. */
function jsonEscape(value: string | string[]): string {
  const v = Array.isArray(value) ? (value.length > 1 ? `(${value.join('|')})` : value[0]) : value;
  return JSON.stringify(String(v)).slice(1, -1);
}
