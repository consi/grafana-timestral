import React from 'react';
import {
  PanelData,
  PluginExtensionAddedLinkConfig,
  PluginExtensionPanelContext,
  PluginExtensionPoints,
} from '@grafana/data';
import { DataQuery } from '@grafana/schema';
import { DS_TYPE, PANEL_ID } from '../constants';
import { ForecastModal, hasTimeSeries } from './ForecastModal';

// Not exported by @grafana/data; mirrors what Explore passes to its toolbar extension point.
type ExploreContext = { exploreId: string; targets: DataQuery[]; data: PanelData; timeZone: string };

export const panelMenuLink: PluginExtensionAddedLinkConfig<PluginExtensionPanelContext> = {
  title: 'Forecast with Timestral',
  description: 'Forecast the series of this panel with Mistral Timestral',
  targets: [PluginExtensionPoints.DashboardPanelMenu],
  icon: 'chart-line',
  category: 'Extensions',
  // Hide on panels without time series, on our own panel (it forecasts inline) and on panels
  // that already show Timestral forecasts.
  configure: (context) => {
    if (!context || context.pluginId === PANEL_ID || !hasTimeSeries(context.data)) {
      return undefined;
    }
    const allTimestral = context.targets.length > 0 && context.targets.every((t) => t.datasource?.type === DS_TYPE);
    return allTimestral ? undefined : {};
  },
  onClick: (_, { context, openModal }) =>
    openModal({
      title: `Forecast · ${context?.title || 'panel'}`,
      width: '80%',
      body: ({ onDismiss }) => (
        <ForecastModal
          id={`panel-menu-${context?.id}`}
          title={context?.title ?? ''}
          data={context?.data}
          targets={context?.targets}
          timeZone={context?.timeZone}
          onDismiss={onDismiss}
        />
      ),
    }),
};

export const exploreToolbarLink: PluginExtensionAddedLinkConfig<ExploreContext> = {
  title: 'Forecast with Timestral',
  description: 'Forecast the current Explore results with Mistral Timestral',
  targets: [PluginExtensionPoints.ExploreToolbarAction],
  icon: 'chart-line',
  onClick: (_, { context, openModal }) =>
    openModal({
      title: 'Forecast · Explore',
      width: '80%',
      body: ({ onDismiss }) => (
        <ForecastModal
          id={`explore-${context?.exploreId}`}
          title="Explore"
          data={context?.data}
          targets={context?.targets}
          timeZone={context?.timeZone}
          onDismiss={onDismiss}
        />
      ),
    }),
};
