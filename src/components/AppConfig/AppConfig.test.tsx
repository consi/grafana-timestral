import React from 'react';
import { render, screen } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import AppConfig, { AppConfigProps } from './AppConfig';

describe('Components/AppConfig', () => {
  test('offers to enable the app when disabled', () => {
    const props = {
      plugin: {
        meta: { id: 'consi-timestral-app', name: 'Timestral', type: PluginType.app, enabled: false, jsonData: {} },
      },
      query: {},
    } as unknown as AppConfigProps;

    render(<AppConfig {...props} />);

    expect(screen.queryByRole('group', { name: /timestral/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /enable/i })).toBeInTheDocument();
  });
});
