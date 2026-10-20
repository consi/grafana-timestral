import React, { useEffect, useState } from 'react';
import { css } from '@emotion/css';
import { CoreApp, DataSourceApi, GrafanaTheme2, QueryEditorProps, TimeRange } from '@grafana/data';
import { DataQuery } from '@grafana/schema';
import { DataSourcePicker, getDataSourceSrv } from '@grafana/runtime';
import {
  Alert,
  Button,
  Combobox,
  IconButton,
  InlineField,
  InlineFieldRow,
  InlineSwitch,
  Input,
  MultiCombobox,
  RadioButtonGroup,
  Stack,
  useStyles2,
} from '@grafana/ui';
import { DS_TYPE } from '../constants';
import { BAND_OPTIONS, CALENDAR_OPTIONS, DIRECTION_OPTIONS, HORIZON_HELP, ROLE_OPTIONS } from '../shared/options';
import { TimestralDataSource } from './datasource';
import { DEFAULT_QUERY, ForecastInput, TimestralOptions, TimestralQuery } from './types';

type Props = QueryEditorProps<TimestralDataSource, TimestralQuery, TimestralOptions>;

const LABEL = 22;

export function QueryEditor({ query: rawQuery, onChange, onRunQuery, range }: Props) {
  const styles = useStyles2(getStyles);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const query = { ...DEFAULT_QUERY, ...rawQuery } as TimestralQuery;
  const update = (patch: Partial<TimestralQuery>, run = false) => {
    onChange({ ...query, ...patch });
    if (run) {
      onRunQuery();
    }
  };
  const updateInput = (i: number, patch: Partial<ForecastInput>, run = false) =>
    update({ inputs: query.inputs.map((input, k) => (k === i ? { ...input, ...patch } : input)) }, run);
  const nextRefId = () => {
    const used = new Set(query.inputs.map((i) => i.refId));
    for (const c of 'ABCDEFGHIJKLMNOPQRSTUVWXYZ') {
      if (!used.has(`in${c}`)) {
        return `in${c}`;
      }
    }
    return `in${query.inputs.length}`;
  };

  return (
    <Stack direction="column" gap={1}>
      {query.inputs.length === 0 && (
        <Alert severity="info" title="Add an input query">
          Pick a data source and write the query to forecast as you normally would. Add more inputs as covariates.
        </Alert>
      )}
      {query.inputs.map((input, i) => (
        <div key={input.refId} className={styles.input}>
          <Stack gap={1} alignItems="center">
            <DataSourcePicker
              width={30}
              current={input.datasource?.uid ?? null}
              noDefault
              metrics
              filter={(ds) => ds.type !== DS_TYPE && !ds.meta.mixed}
              onChange={(ds) => updateInput(i, { datasource: { type: ds.type, uid: ds.uid }, query: undefined })}
            />
            <Combobox
              width={32}
              options={ROLE_OPTIONS}
              value={input.role}
              onChange={(v) => updateInput(i, { role: v.value! }, true)}
            />
            <IconButton
              name="trash-alt"
              tooltip="Remove input"
              onClick={() => update({ inputs: query.inputs.filter((_, k) => k !== i) }, true)}
            />
          </Stack>
          {input.datasource?.uid && (
            <InputQueryEditor
              input={input}
              range={range}
              onChange={(q) => updateInput(i, { query: q })}
              onRunQuery={onRunQuery}
            />
          )}
        </div>
      ))}
      <div>
        <Button
          icon="plus"
          variant="secondary"
          size="sm"
          onClick={() =>
            update({
              inputs: [...query.inputs, { refId: nextRefId(), role: query.inputs.length ? 'past' : 'target' }],
            })
          }
        >
          {query.inputs.length ? 'Add covariate input' : 'Add input'}
        </Button>
      </div>

      <InlineFieldRow>
        <InlineField
          label="Output"
          labelWidth={LABEL}
          tooltip="Forecast series, or time until a threshold is crossed (for alerting)"
        >
          <RadioButtonGroup
            options={[
              { label: 'Forecast', value: 'forecast' },
              { label: 'Time to threshold', value: 'eta' },
            ]}
            value={query.output}
            onChange={(v) => update({ output: v as TimestralQuery['output'] }, true)}
          />
        </InlineField>
        <InlineField label="Horizon" tooltip={HORIZON_HELP}>
          <Input
            width={12}
            value={query.horizon}
            onChange={(e) => update({ horizon: e.currentTarget.value })}
            onBlur={onRunQuery}
          />
        </InlineField>
        <InlineField label="Band" tooltip="Quantile band(s) returned around the median">
          <Combobox
            width={16}
            options={BAND_OPTIONS}
            value={query.band}
            onChange={(v) => update({ band: v.value! }, true)}
          />
        </InlineField>
      </InlineFieldRow>
      <InlineFieldRow>
        {query.output === 'forecast' && (
          <InlineField
            label="Include history"
            labelWidth={LABEL}
            tooltip="Also return the input series (turn off in alert rules)"
          >
            <InlineSwitch
              value={query.includeHistory}
              onChange={(e) => update({ includeHistory: e.currentTarget.checked }, true)}
            />
          </InlineField>
        )}
        <InlineField
          label="Threshold"
          labelWidth={query.output === 'forecast' ? undefined : LABEL}
          tooltip="Value whose crossing time is returned by the time-to-threshold output"
          invalid={query.output === 'eta' && query.threshold === undefined}
        >
          <Stack gap={1}>
            <Input
              type="number"
              width={14}
              value={query.threshold ?? ''}
              onChange={(e) =>
                update({ threshold: e.currentTarget.value === '' ? undefined : Number(e.currentTarget.value) })
              }
              onBlur={onRunQuery}
            />
            <RadioButtonGroup
              options={DIRECTION_OPTIONS}
              value={query.thresholdDirection}
              onChange={(v) => update({ thresholdDirection: v }, true)}
            />
          </Stack>
        </InlineField>
      </InlineFieldRow>

      <div>
        <Button
          variant="secondary"
          fill="text"
          size="sm"
          icon={showAdvanced ? 'angle-down' : 'angle-right'}
          onClick={() => setShowAdvanced(!showAdvanced)}
        >
          Advanced options
        </Button>
      </div>
      {showAdvanced && (
        <>
          <InlineFieldRow>
            <InlineField label="Calendar features" labelWidth={LABEL} tooltip="Known-future covariates from timestamps">
              <MultiCombobox
                width={40}
                options={CALENDAR_OPTIONS}
                value={query.calendar}
                onChange={(v) => update({ calendar: v.map((o) => o.value!) }, true)}
              />
            </InlineField>
            <InlineField
              label="Time zone"
              tooltip="IANA time zone for calendar features, e.g. Europe/Berlin. Empty: the dashboard's; UTC in alert rules."
            >
              <Input
                width={20}
                placeholder="dashboard"
                value={query.timezone ?? ''}
                onChange={(e) => update({ timezone: e.currentTarget.value.trim() || undefined })}
                onBlur={onRunQuery}
              />
            </InlineField>
            <InlineField label="Joint" tooltip="Forecast all target series as variates of one multivariate series">
              <InlineSwitch value={query.joint} onChange={(e) => update({ joint: e.currentTarget.checked }, true)} />
            </InlineField>
            <InlineField label="Max series" tooltip="Only the first N target series are forecast">
              <Input
                type="number"
                width={8}
                value={query.maxSeries}
                onChange={(e) => update({ maxSeries: Number(e.currentTarget.value) || 20 })}
                onBlur={onRunQuery}
              />
            </InlineField>
          </InlineFieldRow>
          <InlineFieldRow>
            <InlineField
              label="Symmetric averaging"
              labelWidth={LABEL}
              tooltip="Average with the reflected forecast (clampsym)"
            >
              <InlineSwitch
                value={query.symmetricAveraging}
                onChange={(e) => update({ symmetricAveraging: e.currentTarget.checked }, true)}
              />
            </InlineField>
            <InlineField label="Clip non-negative" tooltip="Never below 0 for non-negative series">
              <InlineSwitch
                value={query.clipNonnegative}
                onChange={(e) => update({ clipNonnegative: e.currentTarget.checked }, true)}
              />
            </InlineField>
          </InlineFieldRow>
          <InlineFieldRow>
            <InlineField label="Max context" labelWidth={LABEL} tooltip="Most recent steps used as context; 0 = 32768">
              <Input
                type="number"
                width={10}
                value={query.maxContext}
                onChange={(e) => update({ maxContext: Number(e.currentTarget.value) || 0 })}
                onBlur={onRunQuery}
              />
            </InlineField>
            <InlineField label="Block patches" tooltip="Patches decoded per block, 1-32">
              <Input
                type="number"
                width={8}
                value={query.blockPatches}
                onChange={(e) =>
                  update({ blockPatches: Math.min(32, Math.max(1, Number(e.currentTarget.value) || 32)) })
                }
                onBlur={onRunQuery}
              />
            </InlineField>
            <InlineField label="Quantile cap k" tooltip="Clamp to [min - k·s, max + k·s] of the context; 0 disables">
              <Input
                type="number"
                width={10}
                value={query.quantileCapK}
                onChange={(e) => update({ quantileCapK: Number(e.currentTarget.value) })}
                onBlur={onRunQuery}
              />
            </InlineField>
          </InlineFieldRow>
        </>
      )}
    </Stack>
  );
}

/** Renders the input data source's own query editor for the embedded query. */
function InputQueryEditor({
  input,
  range,
  onChange,
  onRunQuery,
}: {
  input: ForecastInput;
  range?: TimeRange;
  onChange: (q: DataQuery) => void;
  onRunQuery: () => void;
}) {
  const [ds, setDs] = useState<DataSourceApi>();
  const [error, setError] = useState<string>();
  const uid = input.datasource?.uid;

  useEffect(() => {
    let cancelled = false;
    getDataSourceSrv()
      .get(uid)
      .then((d) => !cancelled && (setDs(d), setError(undefined)))
      .catch((e) => !cancelled && setError(String(e?.message ?? e)));
    return () => {
      cancelled = true;
    };
  }, [uid]);

  if (error) {
    return (
      <Alert severity="error" title="Cannot load data source">
        {error}
      </Alert>
    );
  }
  if (!ds || ds.uid !== uid) {
    return null;
  }
  const Editor = ds.components?.QueryEditor;
  if (!Editor) {
    return <Alert severity="warning" title={`${ds.name} has no query editor`} />;
  }
  const query = {
    ...(ds.getDefaultQuery?.(CoreApp.PanelEditor) ?? {}),
    ...input.query,
    refId: input.refId,
    datasource: input.datasource,
  } as DataQuery;
  return (
    <Editor
      datasource={ds}
      query={query}
      onChange={onChange}
      onRunQuery={onRunQuery}
      range={range}
      app={CoreApp.PanelEditor}
    />
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  input: css({
    border: `1px solid ${theme.colors.border.weak}`,
    borderRadius: theme.shape.radius.default,
    padding: theme.spacing(1),
    display: 'flex',
    flexDirection: 'column',
    gap: theme.spacing(1),
  }),
});
