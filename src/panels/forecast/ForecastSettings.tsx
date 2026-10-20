import React from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import {
  Checkbox,
  Combobox,
  IconButton,
  InlineField,
  InlineSwitch,
  Input,
  RadioButtonGroup,
  Stack,
  Text,
  Toggletip,
  useStyles2,
} from '@grafana/ui';
import { CALENDAR_OPTIONS, DIRECTION_OPTIONS, ForecastOptions, ROLE_OPTIONS } from './types';

export interface QueryInfo {
  refId: string;
  /** Display name of the query's first series. */
  description: string;
}

interface Props {
  options: ForecastOptions;
  queries: QueryInfo[];
  onChange: (patch: Partial<ForecastOptions>) => void;
}

const LABEL_WIDTH = 22;

/** Every Timestral option, in a popover next to the panel toolbar. */
export const ForecastSettings = ({ options, queries, onChange }: Props) => {
  const styles = useStyles2(getStyles);
  const numberOr = (v: string, fallback: number) => (v === '' || Number.isNaN(Number(v)) ? fallback : Number(v));

  const content = (
    <div className={styles.content}>
      <Stack direction="column" gap={1}>
        <Text variant="h6">Inputs</Text>
        <Text variant="bodySmall" color="secondary">
          Forecast targets, or use a query only as an input. Known-future covariates also need values over the horizon
          (e.g. a schedule); calendar features always have them.
        </Text>
        {queries.map((q) => (
          <InlineField key={q.refId} label={q.refId} labelWidth={8} tooltip={q.description} className={styles.field}>
            <Combobox
              width={36}
              options={ROLE_OPTIONS}
              value={options.roles[q.refId] ?? 'target'}
              onChange={(v) => onChange({ roles: { ...options.roles, [q.refId]: v.value! } })}
            />
          </InlineField>
        ))}
        <InlineField
          label="Calendar features"
          labelWidth={LABEL_WIDTH}
          tooltip="Added as known-future covariates: hour of day and day of week are categorical, weekend is 0/1"
        >
          <Stack gap={2} alignItems="center" wrap="wrap">
            {CALENDAR_OPTIONS.map(({ value: f, label }) => (
              <Checkbox
                key={f}
                label={label}
                value={options.calendar.includes(f)}
                onChange={(e) =>
                  onChange({
                    calendar: e.currentTarget.checked
                      ? [...options.calendar, f]
                      : options.calendar.filter((x) => x !== f),
                  })
                }
              />
            ))}
          </Stack>
        </InlineField>
        <InlineField
          label="Joint forecast"
          labelWidth={LABEL_WIDTH}
          tooltip="Forecast all targets together as the variates of one multivariate series, so they inform each other"
        >
          <InlineSwitch value={options.joint} onChange={(e) => onChange({ joint: e.currentTarget.checked })} />
        </InlineField>

        <Text variant="h6">Analysis</Text>
        <InlineField
          label="Threshold"
          labelWidth={LABEL_WIDTH}
          tooltip="Report when the forecast is expected to cross this value (median and band edge)"
        >
          <Stack gap={1} alignItems="center">
            <Input
              type="number"
              width={14}
              placeholder="none"
              value={options.threshold ?? ''}
              onChange={(e) =>
                onChange({ threshold: e.currentTarget.value === '' ? undefined : Number(e.currentTarget.value) })
              }
            />
            <RadioButtonGroup
              size="sm"
              options={DIRECTION_OPTIONS}
              value={options.thresholdDirection}
              onChange={(v) => onChange({ thresholdDirection: v })}
            />
          </Stack>
        </InlineField>
        <InlineField
          label="Backtest"
          labelWidth={LABEL_WIDTH}
          tooltip="Forecast the last horizon from the history before it, then show accuracy and the points outside the band"
        >
          <InlineSwitch value={options.backtest} onChange={(e) => onChange({ backtest: e.currentTarget.checked })} />
        </InlineField>

        <Text variant="h6">Decoding</Text>
        <InlineField
          label="Symmetric averaging"
          labelWidth={LABEL_WIDTH}
          tooltip="Average with the reflected forecast of the negated series (clampsym); more robust, twice the compute"
        >
          <InlineSwitch
            value={options.symmetricAveraging}
            onChange={(e) => onChange({ symmetricAveraging: e.currentTarget.checked })}
          />
        </InlineField>
        <InlineField
          label="Clip non-negative"
          labelWidth={LABEL_WIDTH}
          tooltip="Never forecast below 0 for series whose history is entirely non-negative"
        >
          <InlineSwitch
            value={options.clipNonnegative}
            onChange={(e) => onChange({ clipNonnegative: e.currentTarget.checked })}
          />
        </InlineField>
        <InlineField
          label="Max context"
          labelWidth={LABEL_WIDTH}
          tooltip="Most recent steps used as context; 0 = 32768"
        >
          <Input
            type="number"
            width={14}
            min={0}
            max={32768}
            value={options.maxContext}
            onChange={(e) => onChange({ maxContext: numberOr(e.currentTarget.value, 0) })}
          />
        </InlineField>
        <InlineField
          label="Block patches"
          labelWidth={LABEL_WIDTH}
          tooltip="Patches (of 32 steps) decoded per block before feeding the median back; 1-32"
        >
          <Input
            type="number"
            width={14}
            min={1}
            max={32}
            value={options.blockPatches}
            onChange={(e) => onChange({ blockPatches: Math.min(32, Math.max(1, numberOr(e.currentTarget.value, 32))) })}
          />
        </InlineField>
        <InlineField
          label="Quantile cap k"
          labelWidth={LABEL_WIDTH}
          tooltip="Clamp forecasts to [min - k·s, max + k·s] of the context (s = latest scale); 0 disables"
        >
          <Input
            type="number"
            width={14}
            min={0}
            value={options.quantileCapK}
            onChange={(e) => onChange({ quantileCapK: numberOr(e.currentTarget.value, 10000) })}
          />
        </InlineField>
      </Stack>
    </div>
  );

  return (
    <Toggletip content={content} placement="bottom-start" closeButton fitContent>
      <IconButton name="cog" tooltip="Forecast settings" aria-label="Forecast settings" />
    </Toggletip>
  );
};

const getStyles = (theme: GrafanaTheme2) => ({
  content: css({ width: 520, maxWidth: '90vw', maxHeight: '70vh', overflowY: 'auto', paddingRight: theme.spacing(1) }),
  field: css({ marginBottom: 0 }),
});
