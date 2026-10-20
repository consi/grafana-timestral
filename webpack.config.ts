import type { Configuration } from 'webpack';
import { merge } from 'webpack-merge';
import CopyWebpackPlugin from 'copy-webpack-plugin';
import grafanaConfig, { type Env } from './.config/webpack/webpack.config';

// The base config only copies the root plugin's logos; copy the nested plugins' assets too.
const config = async (env: Env): Promise<Configuration> => {
  const baseConfig = await grafanaConfig(env);
  return merge(baseConfig, {
    plugins: [
      new CopyWebpackPlugin({
        patterns: [
          { from: 'panels/forecast/img', to: 'panels/forecast/img' },
          { from: 'datasource/img', to: 'datasource/img' },
          { from: '../NOTICE', to: '.' },
        ],
      }),
    ],
  });
};

export default config;
