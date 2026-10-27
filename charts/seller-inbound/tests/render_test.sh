#!/usr/bin/env bash
# Chart render assertions: the env cmd/api reads is the env the chart renders,
# the mandatory configuration has no silent default, no mode switch or log
# publisher is ever rendered, and the shutdown room covers the binary's bounds.
# Run: bash charts/seller-inbound/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=seller-inbound
main_go="$repo_dir/cmd/api/main.go"

# Dummy values, no password: the chart refuses to render without a database
# source, Kafka brokers, the three consumer groups and the SSCC company prefix.
# None is ever dialled.
groups=(nodeConsumerGroup:seller-inbound-node
  catalogConsumerGroup:seller-inbound-catalog
  receiptConsumerGroup:seller-inbound-receipt)
urls=(ssccCompanyPrefix:0614141)
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092)
for g in "${groups[@]}" "${urls[@]}"; do base+=(--set "config.${g%%:*}=${g#*:}"); done
# Every optional value set, so every env var the binary can read is rendered.
everything=(--set config.outboxRelayInterval=2s --set config.shutdownDrainDelay=8s
  --set terminationGracePeriodSeconds=60
  --set config.ssccExtensionDigit=3)

fail() { echo "FAIL: $*" >&2; exit 1; }

configmap="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/configmap.yaml)"
api_deployment="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

# The api Deployment mounts the chart ConfigMap with envFrom, or a value that
# is only in the ConfigMap silently does nothing.
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"

# A tuned config.* value reaches the ConfigMap the api mounts and the container env.
tuned="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned" || fail "config.outboxRelayInterval is not rendered into the ConfigMap"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$tuned" || fail "config.outboxMaxAttempts is not rendered into the ConfigMap"
# The relay attempt cap defaults to 20 and 0 (unlimited) is rendered as is.
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20"
grep -A1 'name: OUTBOX_MAX_ATTEMPTS' <<<"$api_deployment" | grep -q 'value: "20"' \
  || fail "api Deployment does not set OUTBOX_MAX_ATTEMPTS"
unlimited="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.outboxMaxAttempts=0 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"

# Chart vs binary: every env var cmd/api reads must be rendered into the
# Deployment AND the ConfigMap when every optional value is set. The names come
# from the binary's own getenv("...") / env("...") literals plus the
# consumerGroups slice, so a new variable in cmd/api/main.go
# fails this test until the chart renders it.
literal_env() {
  grep -ohE '(Getenv|getenv|env)\("[A-Z][A-Z0-9_]*"' "$main_go" | sed -E 's/.*\("//; s/"$//' | sort -u
}
slice_env() { # slice_env <go slice variable>
  grep -E "^var $1 = " "$main_go" | grep -oE '"[A-Z][A-Z0-9_]*"' | tr -d '"' | sort -u
}
# Word splitting is safe: the names are [A-Z0-9_] only (bash 3.2 has no mapfile).
# shellcheck disable=SC2207
api_vars=($( { literal_env; slice_env consumerGroups; } | sort -u))
(( ${#api_vars[@]} >= 12 )) || fail "found only ${#api_vars[@]} env names in cmd/api/main.go; the scan is looking in the wrong place"
(( $(slice_env consumerGroups | wc -l) == 3 )) || fail "expected 3 consumer groups in cmd/api/main.go, found $(slice_env consumerGroups | wc -l)"
full_deployment="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --show-only templates/deployment.yaml)"
full_configmap="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --show-only templates/configmap.yaml)"
secret_vars=" DATABASE_URL MIGRATIONS_DATABASE_URL "
for var in "${api_vars[@]}"; do
  grep -q "name: $var\$" <<<"$full_deployment" || fail "the api Deployment does not render $var, which cmd/api reads"
  if [[ "$secret_vars" != *" $var "* ]]; then
    grep -q "^  $var: " <<<"$full_configmap" || fail "the ConfigMap does not carry $var, which cmd/api reads"
  fi
done
# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys).
api_known=" ${api_vars[*]} "
while read -r key; do
  [[ "$api_known" == *" $key "* ]] || fail "ConfigMap key $key is read by no code in cmd/api (dead key)"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")
# Every mandatory variable cmd/api names in a boot error is rendered.
for var in $(grep -ho 'errors.New("[A-Z_]* is required' "$main_go" | sed -E 's/errors.New\("//; s/ is required//' | sort -u); do
  grep -q "name: $var\$" <<<"$api_deployment" || fail "the api Deployment does not render $var, which cmd/api requires"
done

# --- the mandatory configuration is rendered from values ------------------------------
env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}
for kv in 'KAFKA_BROKERS=kafka.example.invalid:9092' 'EVENT_PUBLISHER=kafka' \
  'NODE_CONSUMER_GROUP=seller-inbound-node' 'CATALOG_CONSUMER_GROUP=seller-inbound-catalog' \
  'RECEIPT_CONSUMER_GROUP=seller-inbound-receipt' 'SSCC_COMPANY_PREFIX=0614141'; do
  [[ "$(env_value "$api_deployment" "${kv%%=*}")" == "${kv#*=}" ]] || fail "the api Deployment does not render $kv"
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$configmap" || fail "the ConfigMap does not carry $kv"
done
# Optional values are absent by default (the binary's own defaults apply) and rendered when set.
for var in SSCC_EXTENSION_DIGIT OUTBOX_RELAY_INTERVAL SHUTDOWN_DRAIN_DELAY; do
  if grep -q "name: $var\$" <<<"$api_deployment"; then fail "$var must not be rendered by default"; fi
done
for kv in SSCC_EXTENSION_DIGIT=3 OUTBOX_RELAY_INTERVAL=2s SHUTDOWN_DRAIN_DELAY=8s; do
  [[ "$(env_value "$full_deployment" "${kv%%=*}")" == "${kv#*=}" ]] || fail "the api Deployment does not render $kv when set"
done
# DATABASE_URL comes from a Secret, never from the ConfigMap; MIGRATIONS_DATABASE_URL is optional.
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'optional: true' || fail "MIGRATIONS_DATABASE_URL must be an optional secretKeyRef"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
existing="$(helm template "$release" "$chart_dir" --set database.existingSecret=my-db "${base[@]:2}" --show-only templates/deployment.yaml)"
grep -A4 'name: DATABASE_URL$' <<<"$existing" | grep -q 'name: my-db' || fail "database.existingSecret is not used for DATABASE_URL"
existing_all="$(helm template "$release" "$chart_dir" --set database.existingSecret=my-db "${base[@]:2}")"
if grep -q 'kind: Secret' <<<"$existing_all"; then fail "no Secret may be rendered when database.existingSecret is set"; fi
# Pod hardening and probes.
for needle in 'runAsNonRoot: true' 'readOnlyRootFilesystem: true' 'allowPrivilegeEscalation: false' 'type: RuntimeDefault' '- ALL' \
  'path: /healthz' 'path: /readyz'; do
  grep -qF -- "$needle" <<<"$api_deployment" || fail "the api Deployment lacks: $needle"
done
grep -A2 'livenessProbe:' <<<"$api_deployment" | grep -q 'path: /healthz' || fail "liveness must probe /healthz"
grep -A2 'readinessProbe:' <<<"$api_deployment" | grep -q 'path: /readyz' || fail "readiness must probe /readyz"
grep -q 'terminationGracePeriodSeconds: 40' <<<"$api_deployment" || fail "terminationGracePeriodSeconds must default to 40"

# --- observability (ADR 0008, fleet standard item 7) ---------------------------------
# The api renders the fleet pipeline's env: the Collector endpoint, the service
# name, the version (image tag, appVersion when empty) and the environment.
otel_endpoint="otel-collector.observability.svc.cluster.local:4317"
[[ "$(env_value "$api_deployment" OTEL_EXPORTER_OTLP_ENDPOINT)" == "$otel_endpoint" ]] \
  || fail "the api Deployment does not render OTEL_EXPORTER_OTLP_ENDPOINT from otel.endpoint"
[[ "$(env_value "$api_deployment" OTEL_SERVICE_NAME)" == "seller-inbound" ]] \
  || fail "the api Deployment does not render OTEL_SERVICE_NAME=seller-inbound from otel.serviceName"
[[ "$(env_value "$api_deployment" SERVICE_VERSION)" == "0.1.0" ]] || fail "SERVICE_VERSION must default to the chart appVersion"
[[ "$(env_value "$api_deployment" ENVIRONMENT)" == "production" ]] || fail "ENVIRONMENT must come from environment"
tagged="$(helm template "$release" "$chart_dir" "${base[@]}" --set image.tag=v9.9.9 --set otel.serviceName=inbound --set otel.endpoint=http://collector:4317 \
  --set environment=staging --show-only templates/deployment.yaml)"
[[ "$(env_value "$tagged" SERVICE_VERSION)" == "v9.9.9" ]] || fail "SERVICE_VERSION must follow image.tag"
[[ "$(env_value "$tagged" OTEL_SERVICE_NAME)" == "inbound" ]] || fail "OTEL_SERVICE_NAME must follow otel.serviceName"
[[ "$(env_value "$tagged" OTEL_EXPORTER_OTLP_ENDPOINT)" == "http://collector:4317" ]] || fail "OTEL_EXPORTER_OTLP_ENDPOINT must follow otel.endpoint"
[[ "$(env_value "$tagged" ENVIRONMENT)" == "staging" ]] || fail "ENVIRONMENT must follow environment"
# otel.enabled=false renders no OTEL_* env (SERVICE_VERSION stays: it also labels the logs' build).
otel_off="$(helm template "$release" "$chart_dir" "${base[@]}" --set otel.enabled=false --set mcp.enabled=true)"
if grep -q 'name: OTEL_' <<<"$otel_off"; then fail "otel.enabled=false must render no OTEL_* env in any component"; fi
grep -q 'name: SERVICE_VERSION$' <<<"$otel_off" || fail "SERVICE_VERSION must still render with otel.enabled=false"
# The values are chart-owned: extraEnv may not set them a second time.
for owned in OTEL_SERVICE_NAME OTEL_EXPORTER_OTLP_ENDPOINT SERVICE_VERSION ENVIRONMENT; do
  err="$(helm template "$release" "$chart_dir" "${base[@]}" --set "extraEnv[0].name=$owned" --set 'extraEnv[0].value=x' 2>&1)" \
    && fail "extraEnv must not be able to set $owned"
  grep -qF "extraEnv sets $owned" <<<"$err" || fail "the refusal for extraEnv $owned does not name it: $err"
done

# No mode, log publisher or in-memory wording is ever rendered.
render_all="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --set autoscaling.api.enabled=true --set ingress.enabled=true --set gatewayApi.enabled=true)"
if grep -qE '[A-Z]_MODE' <<<"$render_all"; then fail "the render carries a *_MODE variable"; fi
if grep -qiE 'value: "?(permissive|log|off)"?$' <<<"$render_all"; then fail "the render carries permissive/log/off"; fi
if grep -qiE 'in-memory|silently runs|permissive' <<<"$render_all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt mentions an in-memory / permissive mode"
fi
# Only the api component exists.
if grep -E 'app.kubernetes.io/component: ' <<<"$render_all" | grep -qvE 'component: api$'; then
  fail "a component other than api rendered"
fi

# --- refusals: the mandatory values name themselves -----------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
mandatory_except() { # mandatory_except <database|kafka|<config key>>
  local skip="$1" out=()
  [[ "$skip" == database ]] || out+=(--set database.url=postgres://u@h/db)
  [[ "$skip" == kafka ]] || out+=(--set kafka.brokers=k:9092)
  for g in "${groups[@]}" "${urls[@]}"; do
    [[ "${g%%:*}" == "$skip" ]] || out+=(--set "config.${g%%:*}=${g#*:}")
  done
  printf '%s\n' "${out[@]}"
}
refusal_without() { # refusal_without <value to leave out> <needle>
  local args=()
  while IFS= read -r line; do args+=("$line"); done < <(mandatory_except "$1")
  expect_refusal "no $1" "$2" "${args[@]}"
}
refusal_without database "database.url or database.existingSecret"
refusal_without kafka "kafka.brokers"
for g in "${groups[@]}" "${urls[@]}"; do refusal_without "${g%%:*}" "config.${g%%:*}"; done
# An explicitly blank value refuses too.
for g in "${groups[@]}" "${urls[@]}"; do
  expect_refusal "blank ${g%%:*}" "config.${g%%:*}" "${base[@]}" --set "config.${g%%:*}="
done
expect_refusal "the log publisher" "config.eventPublisher" "${base[@]}" --set config.eventPublisher=log
expect_refusal "an unknown publisher" "config.eventPublisher" "${base[@]}" --set config.eventPublisher=nats
expect_refusal "the log publisher through extraEnv" "EVENT_PUBLISHER" "${base[@]}" --set 'extraEnv[0].name=EVENT_PUBLISHER' --set 'extraEnv[0].value=log'
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=NODE_MODE' --set 'extraEnv[0].value=permissive'
# Shutdown room: 5 + 10 + 10 + 10 = 35s, so under 40 is refused.
expect_refusal "a grace period under 40s" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=35
expect_refusal "a grace period of 39s" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=39
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
expect_refusal "a drain delay that eats the grace period" "terminationGracePeriodSeconds" "${base[@]}" --set config.shutdownDrainDelay=30s --set terminationGracePeriodSeconds=40
expect_refusal "a drain delay the chart cannot add up" "config.shutdownDrainDelay" "${base[@]}" --set config.shutdownDrainDelay=1m
helm template "$release" "$chart_dir" "${base[@]}" --set terminationGracePeriodSeconds=40 >/dev/null \
  || fail "a 40s grace period must still render"
helm template "$release" "$chart_dir" "${base[@]}" --set config.shutdownDrainDelay=30s --set terminationGracePeriodSeconds=61 >/dev/null \
  || fail "a 30s drain delay with a 61s grace period must render"

# --- optional component: the read-only MCP server (ADR 0006) -------------------
# The mcp component is OFF by default: a default render is the api alone.
default_render="$(helm template "$release" "$chart_dir" "${base[@]}")"
if grep -q 'app.kubernetes.io/component: mcp$' <<<"$default_render"; then
  fail "the mcp component must not render while mcp.enabled is false"
fi
if grep -q 'app.kubernetes.io/component: frontend$' <<<"$default_render"; then
  fail "the frontend component must not render while frontend.enabled is false"
fi
if grep -q 'seller-inbound-frontend' <<<"$default_render"; then
  fail "the frontend Service/Deployment must not render by default"
fi
grep -q 'app.kubernetes.io/component: api' <<<"$default_render" || fail "the api component is missing from the default render"

# binary_env_of <go files...>: the env literals one binary reads (os.Getenv
# and the telemetry.go env(...) lookups included; comment prose does not match).
binary_env_of() {
  local f
  for f in "$@"; do
    grep -hoE '(Getenv|getenv|env)\("[A-Z][A-Z0-9_]*"' "$f" || true
  done | sed -E 's/.*"([A-Z0-9_]+)".*/\1/' | sort -u
}
# mcp: its OWN env (exactly what cmd/mcp reads), never the api ConfigMap,
# never Kafka, relay, consumer or SSCC env (ADR 0006).
mcp_dep="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --show-only templates/mcp-deployment.yaml)"
grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_dep" || fail "the mcp Deployment was not rendered with mcp.enabled=true"
grep -q 'command: \["/app/mcp"\]' <<<"$mcp_dep" || fail "the mcp Deployment must run /app/mcp"
if grep -q 'configMapRef:' <<<"$mcp_dep"; then fail "mcp-deployment.yaml must not mount the api ConfigMap"; fi
if grep -Eq 'name: (KAFKA_BROKERS|EVENT_PUBLISHER|OUTBOX_[A-Z_]+|SSCC_[A-Z_]+|[A-Z]+_CONSUMER_GROUP)$' <<<"$mcp_dep"; then
  fail "the mcp Deployment must not get Kafka/relay/consumer/SSCC env (read-only, never dials Kafka)"
fi
# shellcheck disable=SC2207
mcp_vars=($(binary_env_of "$repo_dir/cmd/mcp/main.go" "$repo_dir/cmd/mcp/telemetry.go"))
(( ${#mcp_vars[@]} >= 6 )) || fail "found only ${#mcp_vars[@]} env literals in cmd/mcp/main.go; the scan is looking in the wrong place"
mcp_known=" ${mcp_vars[*]} "
for var in "${mcp_vars[@]}" DATABASE_URL MIGRATIONS_DATABASE_URL; do
  grep -q "name: $var\$" <<<"$mcp_dep" || fail "the mcp Deployment does not render $var, which cmd/mcp reads"
done
# No dead keys: every env name the mcp Deployment renders is one cmd/mcp reads.
while read -r name; do
  [[ "$mcp_known" == *" $name "* ]] || fail "the mcp Deployment renders $name, which cmd/mcp does not read"
done < <(grep -oE '^ +- name: [A-Z][A-Z0-9_]+$' <<<"$mcp_dep" | awk '{print $3}' | sort -u)
mcp_versioned="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --set mcp.serviceVersion=dev --show-only templates/mcp-deployment.yaml)"
[[ "$(env_value "$mcp_versioned" SERVICE_VERSION)" == "dev" ]] || fail "SERVICE_VERSION must come from mcp.serviceVersion when set"
[[ "$(env_value "$mcp_dep" SERVICE_VERSION)" == "0.1.0" ]] || fail "the mcp SERVICE_VERSION must default to the image tag / appVersion, like the api"
[[ "$(env_value "$mcp_dep" OTEL_SERVICE_NAME)" == "seller-inbound-mcp" ]] || fail "the mcp component must export as <otel.serviceName>-mcp"
[[ "$(env_value "$mcp_dep" OTEL_EXPORTER_OTLP_ENDPOINT)" == "$otel_endpoint" ]] || fail "the mcp component must export to otel.endpoint"
mcp_named="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --set otel.serviceName=inbound --show-only templates/mcp-deployment.yaml)"
[[ "$(env_value "$mcp_named" OTEL_SERVICE_NAME)" == "inbound-mcp" ]] || fail "the mcp service name must follow otel.serviceName"
# The mcp Service is ClusterIP and selects the mcp component only.
mcp_svc="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --show-only templates/mcp-service.yaml)"
grep -q 'type: ClusterIP' <<<"$mcp_svc" || fail "the mcp Service must stay ClusterIP"
grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_svc" || fail "the mcp Service must select component=mcp"
# The optional host route is off by default and rewrites the prefix to /mcp.
if grep -q 'seller-inbound-mcp' <<<"$default_render"; then fail "the mcp Service/Route must not render by default"; fi
mcp_route="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --set mcp.route.enabled=true --show-only templates/mcp-route.yaml)"
grep -q 'value: /api/mcp/seller-inbound' <<<"$mcp_route" || fail "the mcp HTTPRoute must match the /api/mcp/seller-inbound prefix"
grep -q 'replacePrefixMatch: /mcp' <<<"$mcp_route" || fail "the mcp HTTPRoute must rewrite the prefix to /mcp"

# --- optional component: the console web remote (ADR 0007) ---------------------
# The web remote: its own nginx Deployment + ClusterIP Service, no DB/Kafka
# env, and no route in this chart (the web gateway owns /mfes/seller-inbound/).
frontend_dep="$(helm template "$release" "$chart_dir" "${base[@]}" --set frontend.enabled=true --show-only templates/frontend-deployment.yaml)"
frontend_svc="$(helm template "$release" "$chart_dir" "${base[@]}" --set frontend.enabled=true --show-only templates/frontend-service.yaml)"
grep -q 'app.kubernetes.io/component: frontend' <<<"$frontend_dep" || fail "the frontend Deployment was not rendered with frontend.enabled=true"
grep -q 'type: ClusterIP' <<<"$frontend_svc" || fail "the frontend Service must stay ClusterIP"
if grep -Eq 'name: (DATABASE_URL|KAFKA_BROKERS)$' <<<"$frontend_dep"; then fail "the frontend Deployment must not get database/Kafka env"; fi
if grep -q 'configMapRef:' <<<"$frontend_dep"; then fail "frontend-deployment.yaml must not mount the api ConfigMap"; fi

# ALL components render TOGETHER (the ci/all-components-values.yaml file).
together="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --set frontend.enabled=true)"
for component in api mcp frontend; do
  grep -q "app.kubernetes.io/component: $component\$" <<<"$together" || fail "the full render lacks the $component component"
done
if grep -E 'kind: (Ingress|HTTPRoute)' <<<"$together" | grep -q frontend; then
  fail "frontend routing must not live in this chart"
fi

echo "PASS: seller-inbound chart render assertions"
