import React from 'react';
import { lastValueFrom } from 'rxjs';
import { AppPluginMeta, PluginConfigPageProps } from '@grafana/data';
import { getBackendSrv, locationService } from '@grafana/runtime';
import { Button, FieldSet, Stack, Text } from '@grafana/ui';
import { testIds } from '../testIds';
import { SetupDatasource } from '../SetupDatasource';

export interface AppConfigProps extends PluginConfigPageProps<AppPluginMeta<{}>> {}

/** The model is included, so enabling the app and creating the data source is all the setup. */
const AppConfig = ({ plugin }: AppConfigProps) => {
  const { enabled, pinned } = plugin.meta;

  const setEnabled = async (value: boolean) => {
    await lastValueFrom(
      getBackendSrv().fetch({
        url: `/api/plugins/${plugin.meta.id}/settings`,
        method: 'POST',
        data: { enabled: value, pinned, jsonData: {} },
      })
    );
    // Plugin settings are only read at load time, so reload to propagate them.
    locationService.reload();
  };

  return (
    <FieldSet label="Timestral">
      <Stack direction="column" gap={2}>
        <Text color="secondary">
          The forecasting model is included. Create the data source below to start forecasting.
        </Text>
        {enabled && <SetupDatasource />}
        <div>
          <Button
            data-testid={testIds.appConfig.submit}
            variant={enabled ? 'destructive' : 'primary'}
            onClick={() => setEnabled(!enabled)}
          >
            {enabled ? 'Disable' : 'Enable'}
          </Button>
        </div>
      </Stack>
    </FieldSet>
  );
};

export default AppConfig;
