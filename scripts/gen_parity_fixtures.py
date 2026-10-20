"""Generates reference forecasts from the official PyTorch implementation for the Go parity tests.

Dev-only; the plugin itself has no Python dependency. To regenerate:
  python -m venv .venv && .venv/bin/pip install torch "git+https://github.com/mistralai/timestral.git"
  .venv/bin/python scripts/gen_parity_fixtures.py pkg/timestral/testdata
"""

import json
import sys

import numpy as np
from timestral import TimestralForecaster

out_dir = sys.argv[1]
rng = np.random.default_rng(0)


def walk(*shape):
    return (rng.standard_normal(shape).cumsum(-1) + 10).astype(np.float32)


def seasonal(t, period=48):
    x = np.arange(t)
    return (50 + 20 * np.sin(2 * np.pi * x / period) + rng.standard_normal(t) * 2).astype(np.float32)


def nan_list(a):
    return [None if np.isnan(v) else float(v) for v in np.asarray(a, dtype=np.float64).ravel()]


def nested(a):
    a = np.asarray(a, dtype=np.float64)
    if a.ndim == 1:
        return nan_list(a)
    return [nested(x) for x in a]


cases = []
ctx = walk(1, 1, 500)
cases.append(dict(name="univariate", context=ctx, h=100))

gappy = seasonal(300)[None, None].copy()
gappy[..., :37] = np.nan
gappy[..., 120:131] = np.nan
cases.append(dict(name="gaps_multiblock", context=gappy, h=1500))

cases.append(dict(name="batch_multivariate", context=walk(2, 2, 400), h=64))

t = 384
past = walk(1, 2, t)
cases.append(dict(name="past_covariates", context=seasonal(t)[None, None], h=96, past_covariates=past))

known = np.stack([np.sin(np.arange(t + 96) / 7.0), (np.arange(t + 96) % 48 < 24).astype(np.float32)])[None]
cases.append(dict(name="known_covariates", context=seasonal(t)[None, None], h=96, known_covariates=known.astype(np.float32)))

codes = (np.arange(t + 96) % 48 // 6).astype(np.float64)[None, None]
cases.append(dict(name="categorical", context=seasonal(t)[None, None], h=96,
                  categorical_past_covariates=codes[..., :t], categorical_known_covariates=codes))

neg = (walk(1, 1, 256) - 10).astype(np.float32)
cases.append(dict(name="no_clampsym", context=neg, h=40, symmetric_averaging=False, clip_nonnegative=False))

cases.append(dict(name="options", context=walk(1, 1, 700), h=200, max_context=320, per_shot_horizon_patches=4,
                  quantile_real_cap_k=0.5))

for model_id in ["mistralai/Timestral-209M"]:
    fixtures = []
    for c in cases:
        fc = TimestralForecaster.from_pretrained(
            model_id,
            max_context=c.get("max_context"),
            per_shot_horizon_patches=c.get("per_shot_horizon_patches", 32),
            quantile_real_cap_k=c.get("quantile_real_cap_k", 1e4),
        )
        kwargs = {k: c[k] for k in ["past_covariates", "known_covariates", "categorical_past_covariates",
                                    "categorical_known_covariates", "symmetric_averaging", "clip_nonnegative"] if k in c}
        res = fc.forecast(c["context"], prediction_length=c["h"], **kwargs)
        item = dict(name=c["name"], prediction_length=c["h"], context=nested(c["context"]),
                    quantiles=np.asarray(res.quantiles).astype(np.float64).tolist())
        for k in ["past_covariates", "known_covariates", "categorical_past_covariates", "categorical_known_covariates"]:
            if k in c:
                item[k] = nested(c[k])
        item["options"] = {k: c[k] for k in ["max_context", "per_shot_horizon_patches", "quantile_real_cap_k",
                                             "symmetric_averaging", "clip_nonnegative"] if k in c}
        fixtures.append(item)
        print(model_id, c["name"], np.asarray(res.quantiles).shape, flush=True)
    name = model_id.split("/")[-1]
    with open(f"{out_dir}/parity_{name}.json", "w") as f:
        json.dump({"model": model_id, "cases": fixtures}, f)
