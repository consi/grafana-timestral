import React, { useMemo } from 'react';
import { PanelProps } from '@grafana/data';
import { ForecastView } from './ForecastView';
import { ForecastOptions, withDefaults } from './types';

export const ForecastPanel = (props: PanelProps<ForecastOptions>) => {
  // Query roles are edited in the panel toolbar, so fill anything missing before rendering.
  const options = useMemo(() => withDefaults(props.options), [props.options]);
  return <ForecastView {...props} options={options} />;
};
