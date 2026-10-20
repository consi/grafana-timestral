import React from 'react';
import { AppRootProps } from '@grafana/data';
import { PluginPage } from '@grafana/runtime';
import { Stack, Text } from '@grafana/ui';
import { ModelStatus } from '../ModelStatus';
import { testIds } from '../testIds';

function App(_props: AppRootProps) {
  return (
    <PluginPage>
      <div data-testid={testIds.overview.container}>
        <Stack direction="column" gap={2}>
          <ModelStatus />
          <Text element="h3">Ways to forecast</Text>
          <ul>
            <li>
              <b>Timestral Forecast</b> visualization: keep your queries and add a forecast. Use the cog to set inputs,
              thresholds and backtesting.
            </li>
            <li>
              <b>Forecast with Timestral</b> in any panel menu or in Explore&apos;s <i>Add</i> menu.
            </li>
            <li>
              <b>Timestral data source</b>: forecasts in any visualization and in alert rules.
            </li>
          </ul>
          <Text color="secondary">Tip: longer time ranges give the model more history, and so better forecasts.</Text>
        </Stack>
      </div>
    </PluginPage>
  );
}

export default App;
