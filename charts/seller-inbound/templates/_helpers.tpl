{{/*
Expand the name of the chart.
*/}}
{{- define "seller-inbound.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "seller-inbound.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "seller-inbound.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "seller-inbound.labels" -}}
helm.sh/chart: {{ include "seller-inbound.chart" . }}
{{ include "seller-inbound.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. The Deployment and the Service ALSO pin
app.kubernetes.io/component (api), so a component added later cannot be
selected by accident.
*/}}
{{- define "seller-inbound.selectorLabels" -}}
app.kubernetes.io/name: {{ include "seller-inbound.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "seller-inbound.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "seller-inbound.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "seller-inbound.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "seller-inbound.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference.
*/}}
{{- define "seller-inbound.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
The non-secret environment of cmd/api, as a YAML map: the single source for the
ConfigMap (envFrom) and for the container env, so the two cannot drift. Every
key is a variable cmd/api reads (tests/render_test.sh checks both directions).
DATABASE_URL and MIGRATIONS_DATABASE_URL never appear here: they come from a Secret.
*/}}
{{- define "seller-inbound.configData" -}}
HTTP_ADDR: {{ .Values.config.httpAddr | quote }}
LOG_LEVEL: {{ .Values.config.logLevel | quote }}
EVENT_PUBLISHER: {{ .Values.config.eventPublisher | quote }}
KAFKA_BROKERS: {{ .Values.kafka.brokers | quote }}
NODE_CONSUMER_GROUP: {{ .Values.config.nodeConsumerGroup | quote }}
CATALOG_CONSUMER_GROUP: {{ .Values.config.catalogConsumerGroup | quote }}
RECEIPT_CONSUMER_GROUP: {{ .Values.config.receiptConsumerGroup | quote }}
SSCC_COMPANY_PREFIX: {{ .Values.config.ssccCompanyPrefix | quote }}
OUTBOX_MAX_ATTEMPTS: {{ .Values.config.outboxMaxAttempts | quote }}
{{- with .Values.config.outboxRelayInterval }}
OUTBOX_RELAY_INTERVAL: {{ . | quote }}
{{- end }}
{{- with .Values.config.shutdownDrainDelay }}
SHUTDOWN_DRAIN_DELAY: {{ . | quote }}
{{- end }}
{{- with .Values.config.ssccExtensionDigit }}
SSCC_EXTENSION_DIGIT: {{ . | quote }}
{{- end }}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without DATABASE_URL; there is no in-memory
mode. Fails the render naming the values instead of crash-looping.
*/}}
{{- define "seller-inbound.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "seller-inbound requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without KAFKA_BROKERS and rejects
EVENT_PUBLISHER=log. There is no default broker and no log sink.
*/}}
{{- define "seller-inbound.requireKafka" -}}
{{- $_ := required "seller-inbound requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- if ne (toString .Values.config.eventPublisher) "kafka" -}}
{{- fail (printf "config.eventPublisher is %q: the only publisher is \"kafka\"; the binary rejects any other EVENT_PUBLISHER at boot." (toString .Values.config.eventPublisher)) -}}
{{- end -}}
{{- end -}}

{{/*
The three consumers always run, each under a STABLE consumer group id that
comes from values (cmd/api exits at boot without NODE_CONSUMER_GROUP,
CATALOG_CONSUMER_GROUP and RECEIPT_CONSUMER_GROUP). A stable id is what lets a
rescheduled pod resume from its committed offsets; never derive it from the pod name.
*/}}
{{- define "seller-inbound.requireConsumerGroups" -}}
{{- range $c := list (dict "key" "nodeConsumerGroup" "env" "NODE_CONSUMER_GROUP") (dict "key" "catalogConsumerGroup" "env" "CATALOG_CONSUMER_GROUP") (dict "key" "receiptConsumerGroup" "env" "RECEIPT_CONSUMER_GROUP") -}}
{{- if not (index $.Values.config $c.key) -}}
{{- fail (printf "config.%s is required (%s): the consumer always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE, service-prefixed id, never the pod name." $c.key $c.env) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
SSCC_COMPANY_PREFIX has no default: the network issues SSCCs from its own GS1
company prefix and cmd/api exits at boot without it (6 to 11 digits).
*/}}
{{- define "seller-inbound.requireSscc" -}}
{{- $p := toString .Values.config.ssccCompanyPrefix -}}
{{- if not (regexMatch "^[0-9]{6,11}$" $p) -}}
{{- fail "config.ssccCompanyPrefix is required (SSCC_COMPANY_PREFIX): the network's GS1 company prefix, 6 to 11 digits; cmd/api exits at boot without a valid one." -}}
{{- end -}}
{{- with (toString .Values.config.ssccExtensionDigit) -}}
{{- if not (regexMatch "^[0-9]$" .) -}}
{{- fail (printf "config.ssccExtensionDigit is %q: want a single digit 0 to 9 (SSCC_EXTENSION_DIGIT)." .) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back a log publisher or a mode switch: no EVENT_PUBLISHER
other than kafka and no *_MODE variable at all (this service has no modes).
*/}}
{{- define "seller-inbound.requireNoInMemoryEnv" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- $value := toString (default "" .value) -}}
{{- if and (eq $name "EVENT_PUBLISHER") (ne $value "kafka") -}}
{{- fail (printf "extraEnv sets EVENT_PUBLISHER=%s: the only publisher is \"kafka\"." $value) -}}
{{- end -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "extraEnv sets %s: *_MODE variables are not supported; seller-inbound runs on real Postgres and real Kafka only and has no mode switch." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
The graceful shutdown is sequential (cmd/api/main.go): SHUTDOWN_DRAIN_DELAY
(default 5s) + HTTP drain (10s) + consumers and loops (10s) + outbox relay
(10s) = 35s. The grace period must be at least 40 and above drain delay + 30s,
or the kubelet SIGKILLs the pod between "consumers stopped" and "relay stopped",
stranding an uncommitted offset or an in-flight outbox batch.
config.shutdownDrainDelay must be whole seconds ("5s", "0") so this can be checked.
*/}}
{{- define "seller-inbound.requireGracePeriod" -}}
{{- $drain := 5 -}}
{{- with (toString .Values.config.shutdownDrainDelay) -}}
{{- if regexMatch "^[0-9]+s?$" . -}}
{{- $drain = int (trimSuffix "s" .) -}}
{{- else -}}
{{- fail (printf "config.shutdownDrainDelay is %q: use whole seconds such as \"5s\" so the chart can check terminationGracePeriodSeconds against it." .) -}}
{{- end -}}
{{- end -}}
{{- $need := add $drain 30 -}}
{{- if lt $need 40 -}}{{- $need = 40 -}}{{- end -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) (int $need) -}}
{{- fail (printf "terminationGracePeriodSeconds is %v: want at least %v (SHUTDOWN_DRAIN_DELAY %vs + HTTP drain 10s + consumers and loops 10s + relay 10s, plus margin); a shorter grace period SIGKILLs the consumers or the relay mid-drain." .Values.terminationGracePeriodSeconds $need $drain) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment includes it, so a mis-set release fails `helm template` / `helm install`.
*/}}
{{- define "seller-inbound.requireRealRuntime" -}}
{{- include "seller-inbound.requireDatabase" . -}}
{{- include "seller-inbound.requireKafka" . -}}
{{- include "seller-inbound.requireConsumerGroups" . -}}
{{- include "seller-inbound.requireSscc" . -}}
{{- include "seller-inbound.requireNoInMemoryEnv" . -}}
{{- include "seller-inbound.requireGracePeriod" . -}}
{{- end -}}
