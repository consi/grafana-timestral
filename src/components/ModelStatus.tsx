import React, { useEffect, useState } from 'react';
import { Alert, LoadingPlaceholder } from '@grafana/ui';
import { getStatus, NoDatasourceError, StatusResponse } from '../api';

const POLL_MS = 2000;

/** Load state of the model; polls while it is loading or temporarily unreachable. */
export const ModelStatus = () => {
  const [status, setStatus] = useState<StatusResponse>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = () =>
      getStatus()
        .then((s) => {
          if (cancelled) {
            return;
          }
          setStatus(s);
          setError(undefined);
          if (!s.loaded && !s.error) {
            timer = setTimeout(poll, POLL_MS);
          }
        })
        .catch((e) => {
          if (cancelled) {
            return;
          }
          if (e instanceof NoDatasourceError) {
            setError(e.message);
            return;
          }
          setError(e?.data?.message ?? e?.statusText ?? 'Timestral is unavailable');
          timer = setTimeout(poll, POLL_MS);
        });
    poll();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, []);

  if (error) {
    return (
      <Alert severity="error" title="Timestral is unavailable">
        {error}
      </Alert>
    );
  }
  if (!status) {
    return <LoadingPlaceholder text="Checking model…" />;
  }
  if (status.error) {
    return (
      <Alert severity="error" title="The model failed to load">
        {status.error}
      </Alert>
    );
  }
  if (!status.loaded) {
    return <LoadingPlaceholder text="Loading the model…" />;
  }
  return <Alert severity="success" title="Timestral is ready" />;
};
