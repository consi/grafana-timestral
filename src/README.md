# Timestral

![Forecast with a quantile fan](https://raw.githubusercontent.com/consi/grafana-timestral/main/src/img/screenshots/forecast-fan.jpg)

Forecast any time series in Grafana with Mistral's Timestral model. You don't need to set anything up
or train a model.

## Features

- **Forecast panel**: your queries plus a forecast with confidence bands.
- **Forecast any panel or Explore result**: _Forecast with Timestral_ in the panel menu or in
  Explore's _Add_ menu, then add it to a dashboard.
- **Data source for alerting**: alert on forecasts, e.g. _disk usage will cross 90% within 6 hours_.
- **Better forecasts with context**: use related queries (e.g. request rate for CPU) and
  hour-of-day or day-of-week patterns.
- **Capacity planning**: when a series is expected to cross a threshold.
- **Backtesting**: see how accurate past forecasts were, and spot anomalies.

## Getting started

1. Enable the app.
2. On its configuration page, click **Create data source and service account**.
3. Pick the **Timestral Forecast** visualization, or use _Forecast with Timestral_ from any panel menu.

## License

The plugin is Apache-2.0. It includes Mistral AI's Timestral-209M model under its Modified MIT License
(`model/LICENSE`), which doesn't cover companies with more than USD 20 million in monthly revenue,
or their employees. Not affiliated with Mistral AI.
