import { DataSourcePlugin } from '@grafana/data';
import { ConfigEditor } from './ConfigEditor';
import { TimestralDataSource } from './datasource';
import { QueryEditor } from './QueryEditor';
import { TimestralOptions, TimestralQuery } from './types';

export const plugin = new DataSourcePlugin<TimestralDataSource, TimestralQuery, TimestralOptions>(TimestralDataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
