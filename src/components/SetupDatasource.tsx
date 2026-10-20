import React, { useEffect, useState } from 'react';
import { lastValueFrom } from 'rxjs';
import { getBackendSrv } from '@grafana/runtime';
import { Alert, Button, Stack, Text, TextLink } from '@grafana/ui';
import { DS_TYPE } from '../constants';

const SA_NAME = 'timestral-datasource';

interface DatasourceInfo {
  uid: string;
  name: string;
}

async function api<T>(method: string, url: string, data?: unknown): Promise<T> {
  const res = await lastValueFrom(getBackendSrv().fetch<T>({ method, url, data, showErrorAlert: false }));
  return res.data;
}

/**
 * Creates the Timestral data source with a Viewer service account token, which it needs to run
 * its input queries (also from alert rules). Requires an admin session.
 */
export const SetupDatasource = () => {
  const [existing, setExisting] = useState<DatasourceInfo[]>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let cancelled = false;
    api<Array<DatasourceInfo & { type: string }>>('GET', '/api/datasources')
      .then((all) => !cancelled && setExisting(all.filter((d) => d.type === DS_TYPE)))
      .catch((e) => {
        if (!cancelled) {
          setExisting([]);
          setError(e?.data?.message ?? 'Cannot list data sources; an admin needs to do the setup');
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const setup = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const found = await api<{ serviceAccounts: Array<{ id: number; name: string }> }>(
        'GET',
        `/api/serviceaccounts/search?query=${SA_NAME}`
      );
      const sa =
        found.serviceAccounts.find((a) => a.name === SA_NAME) ??
        (await api<{ id: number }>('POST', '/api/serviceaccounts', { name: SA_NAME, role: 'Viewer' }));
      const token = await api<{ key: string }>('POST', `/api/serviceaccounts/${sa.id}/tokens`, {
        name: `timestral-${Date.now()}`,
      });
      const created = await api<{ datasource: DatasourceInfo }>('POST', '/api/datasources', {
        name: 'Timestral',
        // A fixed uid lets provisioned dashboards and alert rules reference it.
        uid: 'timestral',
        type: DS_TYPE,
        access: 'proxy',
        jsonData: {},
        secureJsonData: { serviceAccountToken: token.key },
      });
      setExisting([created.datasource]);
    } catch (e: any) {
      setError(e?.data?.message ?? e?.message ?? 'Setup failed');
    } finally {
      setBusy(false);
    }
  };

  if (!existing) {
    return null;
  }
  return (
    <Stack direction="column" gap={1}>
      <Text variant="h5">Timestral data source</Text>
      <Text color="secondary">
        Forecast any query in dashboards and <b>alert rules</b>: the data source runs other data sources&apos; queries
        and returns forecast series or the time until a threshold is crossed.
      </Text>
      {existing.length > 0 ? (
        <Alert severity="success" title="Data source ready">
          {existing.map((d) => (
            <TextLink key={d.uid} href={`/connections/datasources/edit/${d.uid}`} inline>
              {d.name}
            </TextLink>
          ))}
        </Alert>
      ) : (
        <div>
          <Button icon="plus" onClick={setup} disabled={busy}>
            {busy ? 'Setting up…' : 'Create data source and service account'}
          </Button>
        </div>
      )}
      {error && (
        <Alert severity="error" title="Setup failed">
          {error}
        </Alert>
      )}
    </Stack>
  );
};
