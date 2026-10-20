import { PanelPlugin } from '@grafana/data';
import { ForecastPanel } from './ForecastPanel';
import {
  BAND_OPTIONS,
  DEFAULT_OPTIONS,
  DIRECTION_OPTIONS,
  ForecastOptions,
  HORIZON_HELP,
  HORIZON_PRESETS,
  withDefaults,
} from './types';

const d = DEFAULT_OPTIONS;

export const plugin = new PanelPlugin<ForecastOptions>(ForecastPanel)
  .useFieldConfig()
  .setMigrationHandler((panel) => withDefaults(panel.options ?? {}))
  .setPanelOptions((builder) =>
    builder
      .addBooleanSwitch({ path: 'enabled', name: 'Show forecast', defaultValue: d.enabled, category: ['Forecast'] })
      .addSelect({
        path: 'horizon',
        name: 'Horizon',
        description: HORIZON_HELP,
        defaultValue: d.horizon,
        category: ['Forecast'],
        settings: { allowCustomValue: true, options: HORIZON_PRESETS.map((h) => ({ label: h, value: h })) },
      })
      .addSelect({
        path: 'band',
        name: 'Confidence band',
        description: 'Quantile band(s) drawn around the median forecast',
        defaultValue: d.band,
        category: ['Forecast'],
        settings: { options: BAND_OPTIONS },
      })
      .addSliderInput({
        path: 'fillOpacity',
        name: 'Band opacity',
        defaultValue: d.fillOpacity,
        category: ['Forecast'],
        settings: { min: 0, max: 100, step: 5 },
        showIf: (o) => o.band !== 'none',
      })
      .addNumberInput({
        path: 'maxSeries',
        name: 'Max series',
        description: 'Only the first N target series are forecast',
        defaultValue: d.maxSeries,
        category: ['Forecast'],
        settings: { min: 1, max: 64, integer: true },
      })
      .addBooleanSwitch({
        path: 'showToolbar',
        name: 'Show toolbar',
        defaultValue: d.showToolbar,
        category: ['Forecast'],
      })
      .addBooleanSwitch({
        path: 'joint',
        name: 'Joint forecast',
        description: 'Forecast all targets as the variates of one multivariate series',
        defaultValue: d.joint,
        category: ['Inputs'],
      })
      .addNumberInput({
        path: 'threshold',
        name: 'Threshold',
        description: 'Report when the forecast is expected to cross this value',
        category: ['Analysis'],
      })
      .addRadio({
        path: 'thresholdDirection',
        name: 'Threshold direction',
        defaultValue: d.thresholdDirection,
        category: ['Analysis'],
        settings: { options: DIRECTION_OPTIONS },
      })
      .addBooleanSwitch({
        path: 'backtest',
        name: 'Backtest',
        description: 'Forecast the last horizon from the history before it and score it against what happened',
        defaultValue: d.backtest,
        category: ['Analysis'],
      })
      .addBooleanSwitch({
        path: 'symmetricAveraging',
        name: 'Symmetric averaging',
        description: 'Average with the reflected forecast of the negated series (more robust, twice the compute)',
        defaultValue: d.symmetricAveraging,
        category: ['Decoding'],
      })
      .addBooleanSwitch({
        path: 'clipNonnegative',
        name: 'Clip non-negative',
        description: 'Never forecast below 0 for series whose history is non-negative',
        defaultValue: d.clipNonnegative,
        category: ['Decoding'],
      })
      .addNumberInput({
        path: 'maxContext',
        name: 'Max context',
        description: 'Most recent steps used as context; 0 = 32768',
        defaultValue: d.maxContext,
        category: ['Decoding'],
        settings: { min: 0, max: 32768, integer: true },
      })
      .addNumberInput({
        path: 'blockPatches',
        name: 'Block patches',
        description: 'Patches (32 steps each) decoded per block; 1-32',
        defaultValue: d.blockPatches,
        category: ['Decoding'],
        settings: { min: 1, max: 32, integer: true },
      })
      .addNumberInput({
        path: 'quantileCapK',
        name: 'Quantile cap k',
        description: 'Clamp to [min - k·s, max + k·s] of the context; 0 disables',
        defaultValue: d.quantileCapK,
        category: ['Decoding'],
        settings: { min: 0 },
      })
  );
