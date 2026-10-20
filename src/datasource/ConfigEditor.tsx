import React from 'react';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { Field, FieldSet, Input, SecretInput, Stack, Text, TextLink } from '@grafana/ui';
import { PLUGIN_ID } from '../constants';
import { TimestralOptions, TimestralSecureOptions } from './types';

type Props = DataSourcePluginOptionsEditorProps<TimestralOptions, TimestralSecureOptions>;

export function ConfigEditor({ options, onOptionsChange }: Props) {
  const { jsonData, secureJsonFields, secureJsonData } = options;
  return (
    <FieldSet label="Timestral">
      <Stack direction="column" gap={1}>
        <Text color="secondary">
          Timestral runs the input queries of its forecasts through Grafana&apos;s query API (also in alert rules, where
          there is no user), so it needs a service account token with the <b>Viewer</b> role. The Timestral app&apos;s{' '}
          <TextLink href={`/plugins/${PLUGIN_ID}`} inline>
            configuration page
          </TextLink>{' '}
          can create both for you.
        </Text>
        <Field label="Service account token" description="Token of a service account with the Viewer role">
          <SecretInput
            width={60}
            isConfigured={Boolean(secureJsonFields?.serviceAccountToken)}
            value={secureJsonData?.serviceAccountToken ?? ''}
            placeholder="glsa_…"
            onChange={(e) =>
              onOptionsChange({
                ...options,
                secureJsonData: { ...secureJsonData, serviceAccountToken: e.currentTarget.value },
              })
            }
            onReset={() =>
              onOptionsChange({
                ...options,
                secureJsonFields: { ...secureJsonFields, serviceAccountToken: false },
                secureJsonData: { ...secureJsonData, serviceAccountToken: '' },
              })
            }
          />
        </Field>
        <Field
          label="Grafana URL"
          description="How the Grafana server reaches its own API. Leave empty to use Grafana's configured root URL."
        >
          <Input
            width={60}
            placeholder="http://localhost:3000"
            value={jsonData.grafanaUrl ?? ''}
            onChange={(e) =>
              onOptionsChange({ ...options, jsonData: { ...jsonData, grafanaUrl: e.currentTarget.value } })
            }
          />
        </Field>
      </Stack>
    </FieldSet>
  );
}
