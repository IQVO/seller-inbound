#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are identical
on every pod a release creates. A Service that selects on those two alone selects
ALL of them (a Kong request for an api /healthz was once answered by the wrong
pod). Every Deployment carries `app.kubernetes.io/component` in selector.matchLabels
and its pod labels, and every Service selects on it, so each Service selects
EXACTLY ONE Deployment. This test fails if that ever stops being true.

This chart has three components: api (always), the optional, default-off mcp
(ADR 0006, the read-only MCP server) and the optional, default-off frontend
(the nginx pod serving the seller_inbound_mfe remote, ADR 0007), rendered
TOGETHER in their checks below. The frontend is never routed by this chart.
The optional HPA, Ingress and api HTTPRoute are turned on together and
must all point at the api workload. Run:
python3 charts/seller-inbound/tests/test_service_selectors.py
Needs: helm, PyYAML (a missing PyYAML prints SKIP, which CI treats as a failure).
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "seller-inbound"

GROUPS = {
    "nodeConsumerGroup": "seller-inbound-node",
    "catalogConsumerGroup": "seller-inbound-catalog",
    "receiptConsumerGroup": "seller-inbound-receipt",
}
URLS = {
    "ssccCompanyPrefix": "0614141",
}


def runtime(skip: str | None = None) -> list[str]:
    """The mandatory values, minus the one named by skip (database, kafka or a config key)."""
    args: list[str] = []
    if skip != "database":
        args += ["--set", "database.url=postgres://u@example.invalid:5432/db"]
    if skip != "kafka":
        args += ["--set", "kafka.brokers=kafka.example.invalid:9092"]
    for key, value in {**GROUPS, **URLS}.items():
        if skip != key:
            args += ["--set", f"config.{key}={value}"]
    return args


BASE = runtime()
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "config.ssccExtensionDigit=3",
]
# The mcp component (ADR 0006), rendered together with its optional host route.
ENABLE_MCP = BASE + [
    "--set", "mcp.enabled=true",
    "--set", "mcp.route.enabled=true",
]
# The frontend component (ADR 0007): the nginx pod of the seller_inbound_mfe
# web remote, rendered together with the api it belongs to.
ENABLE_FRONTEND = BASE + [
    "--set", "frontend.enabled=true",
]

# Exactly the env vars cmd/mcp (main.go, telemetry.go) reads (its own contract;
# the api ConfigMap is deliberately not mounted on that Deployment).
MCP_ENV = {"MCP_ADDR", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT", "DATABASE_URL", "MIGRATIONS_DATABASE_URL",
           "OTEL_SERVICE_NAME", "OTEL_EXPORTER_OTLP_ENDPOINT"}
# Never rendered on the mcp Deployment: the binary is read-only and never
# dials Kafka.
MCP_STRAY = {"KAFKA_BROKERS", "EVENT_PUBLISHER", "SSCC_COMPANY_PREFIX", "SSCC_EXTENSION_DIGIT",
             "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS",
             "NODE_CONSUMER_GROUP", "CATALOG_CONSUMER_GROUP", "RECEIPT_CONSUMER_GROUP"}


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors")
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE}:
        failures.append(f"expected exactly the api Deployment {RELEASE!r}, got {sorted(deployments)}")
    if set(services) != {RELEASE}:
        failures.append(f"expected exactly the api Service {RELEASE!r}, got {sorted(services)}")
    if RELEASE in services and selector_of(services[RELEASE]).get("app.kubernetes.io/component") != "api":
        failures.append("the api Service selector must pin component=api")

    # Every Deployment's own selector must pin a component too, and be satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # The HPA owns replicas when enabled and scales the api Deployment.
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in deployments.get(RELEASE, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    # The Ingress and the HTTPRoute route to the api Service.
    ingress = next((d for d in docs if d.get("kind") == "Ingress"), None)
    if ingress is None:
        failures.append("the Ingress was not rendered with ingress.enabled=true")
    else:
        for rule in ingress["spec"]["rules"]:
            for path in rule["http"]["paths"]:
                if path["backend"]["service"]["name"] not in services:
                    failures.append("the Ingress backend is not a Service of this chart")
    route = next((d for d in docs if d.get("kind") == "HTTPRoute"), None)
    if route is None:
        failures.append("the HTTPRoute was not rendered with gatewayApi.enabled=true")
    else:
        for rule in route["spec"]["rules"]:
            for ref in rule["backendRefs"]:
                if ref["name"] not in services:
                    failures.append("the HTTPRoute backend is not a Service of this chart")


def check_mcp(docs: list[dict], failures: list[str]) -> None:
    """The mcp component (ADR 0006): its own Deployment + ClusterIP Service
    (component=mcp pinned everywhere), its own env contract, and its optional
    host route targeting the mcp Service."""
    name = f"{RELEASE}-mcp"
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE, name}:
        failures.append(f"with mcp.enabled: expected exactly the api and mcp Deployments, got {sorted(deployments)}")
    if set(services) != {RELEASE, name}:
        failures.append(f"with mcp.enabled: expected exactly the api and mcp Services, got {sorted(services)}")

    # The real invariant, again: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    if name in services and selector_of(services[name]).get("app.kubernetes.io/component") != "mcp":
        failures.append("the mcp Service selector must pin component=mcp")
    if name not in deployments:
        failures.append("the mcp Deployment was not rendered with mcp.enabled=true")
        return
    if deployments[name]["spec"]["selector"].get("matchLabels", {}).get("app.kubernetes.io/component") != "mcp":
        failures.append("the mcp Deployment selector.matchLabels must pin component=mcp")
    if not matches(deployments[name]["spec"]["selector"].get("matchLabels", {}), pod_labels_of(deployments[name])):
        failures.append("the mcp Deployment pod labels do not satisfy its own selector")

    containers = deployments[name]["spec"]["template"]["spec"]["containers"]
    command = containers[0].get("command", []) if containers else []
    if command != ["/app/mcp"]:
        failures.append(f"the mcp Deployment must run /app/mcp, got {command}")
    env = {e["name"] for e in (containers[0].get("env", []) if containers else []) if "name" in e}
    if env != MCP_ENV:
        failures.append(f"the mcp Deployment env must be exactly {sorted(MCP_ENV)}, got {sorted(env)}")
    stray = MCP_STRAY & env
    if stray:
        failures.append(f"the mcp Deployment must not get Kafka/relay/consumer/SSCC env: {sorted(stray)}")

    route = next((d for d in docs if d.get("kind") == "HTTPRoute" and d["metadata"]["name"] == name), None)
    if route is None:
        failures.append("the mcp HTTPRoute was not rendered with mcp.route.enabled=true")
    else:
        for rule in route["spec"]["rules"]:
            for ref in rule["backendRefs"]:
                if ref["name"] != name:
                    failures.append("the mcp HTTPRoute backend is not the mcp Service")


def check_frontend(docs: list[dict], failures: list[str]) -> None:
    """The frontend component (ADR 0007): its own nginx Deployment + ClusterIP
    Service (component=frontend pinned everywhere), zero env (it serves
    static bytes; no database, no Kafka), and no route of this chart naming
    it (the web gateway owns /mfes/seller-inbound/)."""
    name = f"{RELEASE}-frontend"
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE, name}:
        failures.append(f"with frontend.enabled: expected exactly the api and frontend Deployments, got {sorted(deployments)}")
    if set(services) != {RELEASE, name}:
        failures.append(f"with frontend.enabled: expected exactly the api and frontend Services, got {sorted(services)}")

    # The real invariant, again: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    if name in services:
        if selector_of(services[name]).get("app.kubernetes.io/component") != "frontend":
            failures.append("the frontend Service selector must pin component=frontend")
        if services[name]["spec"].get("type") != "ClusterIP":
            failures.append("the frontend Service must be ClusterIP")
    else:
        failures.append("the frontend Service must exist with frontend.enabled=true")
    if name not in deployments:
        failures.append("the frontend Deployment was not rendered with frontend.enabled=true")
        return
    if deployments[name]["spec"]["selector"].get("matchLabels", {}).get("app.kubernetes.io/component") != "frontend":
        failures.append("the frontend Deployment selector.matchLabels must pin component=frontend")
    if not matches(deployments[name]["spec"]["selector"].get("matchLabels", {}), pod_labels_of(deployments[name])):
        failures.append("the frontend Deployment pod labels do not satisfy its own selector")

    containers = deployments[name]["spec"]["template"]["spec"]["containers"]
    env = [e["name"] for e in (containers[0].get("env", []) if containers else []) if "name" in e]
    if env:
        failures.append(f"the frontend Deployment must render no env at all (it serves static bytes), got {sorted(env)}")
    ports = {p["name"] for p in (containers[0].get("ports", []) if containers else [])}
    if "http" not in ports:
        failures.append("the frontend Deployment does not expose the named port 'http'")

    for d in docs:
        if d.get("kind") in {"Ingress", "HTTPRoute"} and "frontend" in d["metadata"]["name"]:
            failures.append(f"{d['kind']} {d['metadata']['name']}: frontend routing must not live in this chart")


def check_refusals(failures: list[str]) -> None:
    cases: list[tuple[str, list[str], str]] = [
        ("without a database source", runtime("database"), "requires database.url or database.existingSecret"),
        ("without kafka.brokers", runtime("kafka"), "kafka.brokers"),
        ("the log publisher", BASE + ["--set", "config.eventPublisher=log"], "the only publisher is"),
        ("an unknown publisher", BASE + ["--set", "config.eventPublisher=nats"], "the only publisher is"),
        ("EVENT_PUBLISHER=log through extraEnv",
         BASE + ["--set", "extraEnv[0].name=EVENT_PUBLISHER", "--set", "extraEnv[0].value=log"], "extraEnv sets EVENT_PUBLISHER=log"),
        ("a *_MODE through extraEnv",
         BASE + ["--set", "extraEnv[0].name=NODE_MODE", "--set", "extraEnv[0].value=permissive"], "extraEnv sets NODE_MODE"),
        ("a grace period under 40s", BASE + ["--set", "terminationGracePeriodSeconds=35"], "terminationGracePeriodSeconds"),
    ]
    for key in {**GROUPS, **URLS}:
        cases.append((f"without config.{key}", runtime(key), f"config.{key} is required"))
    for label, args, needle in cases:
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_components(render(ENABLE_EVERYTHING), failures)
    check_mcp(render(ENABLE_MCP), failures)
    check_frontend(render(ENABLE_FRONTEND), failures)

    # Default values must not deploy an HPA, a route, an Ingress, the mcp or
    # the frontend.
    defaults = render(BASE)
    stray = [d["kind"] for d in defaults if d.get("kind") in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"}]
    if stray:
        failures.append(f"optional resources rendered with default values: {stray}")
    if any(d.get("metadata", {}).get("name", "").endswith("-mcp") for d in defaults):
        failures.append("the mcp component must not render with default values (mcp.enabled=false)")
    if any(d.get("metadata", {}).get("name", "").endswith("-frontend") for d in defaults):
        failures.append("the frontend component must not render with default values (frontend.enabled=false)")

    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: the api Service selects exactly one Deployment (component=api) and so does the mcp Service "
          "(component=mcp, ADR 0006, with exactly the env cmd/mcp reads) and the frontend Service "
          "(component=frontend, ADR 0007, ClusterIP, no env, never routed by this chart); the HPA, Ingress, "
          "HTTPRoute and the mcp and frontend components are off by default and target their own component; "
          "the chart refuses to render without a "
          "database source, brokers, any consumer group or neighbour URL, with a log/unknown publisher or a "
          "*_MODE in extraEnv, or with a grace period under 40s")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
