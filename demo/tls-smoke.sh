#!/bin/sh
set -eu

compose="docker compose -f compose.yaml -f compose.tls-demo.yaml"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

ca_bundle="$tmpdir/caddy-local-root.crt"
deadline=$(( $(date +%s) + ${READY_TIMEOUT_SECONDS:-180} ))

fail() {
  printf '%s\n' "TLS smoke failed: $*"
  printf '%s\n' 'TLS compose service status:'
  $compose ps 2>&1 || true
  exit 1
}

gateway_container_id="$($compose ps -q payment-gateway)"
if [ -z "$gateway_container_id" ]; then
  fail 'payment-gateway container is not running; start the TLS demo with make demo-tls'
fi

gateway_host_ports="$(docker inspect --format '{{range $port, $bindings := .NetworkSettings.Ports}}{{if $bindings}}{{$port}}={{(index $bindings 0).HostPort}} {{end}}{{end}}' "$gateway_container_id")"
if [ -n "$gateway_host_ports" ]; then
  fail "payment-gateway has published host port binding(s): $gateway_host_ports; expected none"
fi

while :; do
  if $compose exec -T caddy test -s /data/caddy/pki/authorities/local/root.crt 2>/dev/null; then
    $compose exec -T caddy cat /data/caddy/pki/authorities/local/root.crt > "$ca_bundle"
    break
  fi

  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail 'Caddy did not create its local development CA certificate'
  fi

  sleep 1
done

wait_for_https_status() {
  path="$1"
  expected="$2"
  response_file="$3"

  while :; do
    status="$(curl --cacert "$ca_bundle" -sS -o "$response_file" -w '%{http_code}' "https://localhost:8443$path" 2>/dev/null)" || status="unreachable"
    if [ "$status" = "$expected" ]; then
      return
    fi

    if [ "$(date +%s)" -ge "$deadline" ]; then
      printf '%s\n' "HTTPS $path returned $status, expected $expected"
      cat "$response_file" 2>/dev/null || true
      fail 'HTTPS edge did not become ready'
    fi

    sleep 1
  done
}

wait_for_https_status /healthz 204 "$tmpdir/healthz"

status="$(curl --cacert "$ca_bundle" -sS -o "$tmpdir/readyz" -w '%{http_code}' https://localhost:8443/readyz)"
test "$status" = "204" || {
  fail "HTTPS /readyz returned $status, expected 204"
}

status="$(curl --cacert "$ca_bundle" -sS -o "$tmpdir/not-proxied" -w '%{http_code}' https://localhost:8443/not-proxied)"
test "$status" = "404" || {
  fail "HTTPS /not-proxied returned $status, expected 404"
}

dd if=/dev/zero of="$tmpdir/oversized-request" bs=1 count=65537 2>/dev/null
status="$(curl --cacert "$ca_bundle" -sS -o "$tmpdir/oversized-request-response" -w '%{http_code}' -X POST --data-binary "@$tmpdir/oversized-request" https://localhost:8443/v1/payments/authorize)"
test "$status" = "413" || {
  fail "HTTPS oversized request returned $status, expected 413"
}

redirect="$(curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' http://localhost:8080/healthz)"
test "$redirect" = "308 https://localhost:8443/healthz" || {
  fail "HTTP /healthz redirect was '$redirect', expected '308 https://localhost:8443/healthz'"
}

BASE_URL=https://localhost:8443 CURL_CA_BUNDLE="$ca_bundle" ./demo/smoke.sh
