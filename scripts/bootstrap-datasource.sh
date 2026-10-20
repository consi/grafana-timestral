#!/usr/bin/env sh
# Dev stack only: creates the Timestral data source (uid "timestral") with a Viewer service
# account token, like the app's "Create data source and service account" button does. The
# provisioned demo dashboard and alert rule use this uid.
set -eu
GRAFANA="${GRAFANA_URL:-http://localhost:3000}"
AUTH="${GRAFANA_AUTH:-}" # e.g. "-u admin:admin"; the dev stack allows anonymous admin

api() { curl -sf $AUTH -H 'Content-Type: application/json' "$@"; }
fail() { echo "$1" >&2; exit 1; }

if api "$GRAFANA/api/datasources/uid/timestral" >/dev/null 2>&1; then
  echo "Timestral data source already exists"
  exit 0
fi
accounts=$(api "$GRAFANA/api/serviceaccounts/search?query=timestral-datasource") || fail "cannot list service accounts"
SA_ID=$(echo "$accounts" | tr '{' '\n' | grep '"name":"timestral-datasource"' | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -n 1)
if [ -z "$SA_ID" ]; then
  created=$(api -X POST "$GRAFANA/api/serviceaccounts" -d '{"name":"timestral-datasource","role":"Viewer"}') ||
    fail "cannot create the service account"
  SA_ID=$(echo "$created" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
fi
[ -n "$SA_ID" ] || fail "no service account id"
token=$(api -X POST "$GRAFANA/api/serviceaccounts/$SA_ID/tokens" -d "{\"name\":\"timestral-$(date +%s)\"}") ||
  fail "cannot create a token"
TOKEN=$(echo "$token" | sed -n 's/.*"key":"\([^"]*\)".*/\1/p')
[ -n "$TOKEN" ] || fail "no token in the response"
api -X POST "$GRAFANA/api/datasources" \
  -d "{\"name\":\"Timestral\",\"uid\":\"timestral\",\"type\":\"consi-timestral-datasource\",\"access\":\"proxy\",\"secureJsonData\":{\"serviceAccountToken\":\"$TOKEN\"}}" \
  >/dev/null || fail "cannot create the data source"
echo "Created Timestral data source (uid timestral)"
