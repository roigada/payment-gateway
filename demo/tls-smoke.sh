#!/bin/sh
set -eu

compose="docker compose -f compose.yaml -f compose.tls-demo.yaml"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

ca_bundle="$tmpdir/caddy-local-root.crt"
deadline=$(( $(date +%s) + ${READY_TIMEOUT_SECONDS:-180} ))

gateway_published_ports="$($compose config --format json | jq '.services["payment-gateway"].ports // [] | length')"
test "$gateway_published_ports" = "0" || {
  printf '%s\n' "TLS smoke failed: payment-gateway has $gateway_published_ports published host port(s), expected none"
  exit 1
}

while :; do
  if $compose exec -T caddy test -s /data/caddy/pki/authorities/local/root.crt 2>/dev/null; then
    $compose exec -T caddy cat /data/caddy/pki/authorities/local/root.crt > "$ca_bundle"
    break
  fi

  if [ "$(date +%s)" -ge "$deadline" ]; then
    printf '%s\n' 'TLS smoke failed: Caddy did not create its local development CA certificate'
    exit 1
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
      printf '%s\n' "TLS smoke failed: HTTPS $path returned $status, expected $expected"
      cat "$response_file" 2>/dev/null || true
      exit 1
    fi

    sleep 1
  done
}

wait_for_https_status /healthz 204 "$tmpdir/healthz"

status="$(curl --cacert "$ca_bundle" -sS -o "$tmpdir/readyz" -w '%{http_code}' https://localhost:8443/readyz)"
test "$status" = "204" || {
  printf '%s\n' "TLS smoke failed: HTTPS /readyz returned $status, expected 204"
  exit 1
}

status="$(curl --cacert "$ca_bundle" -sS -o "$tmpdir/not-proxied" -w '%{http_code}' https://localhost:8443/not-proxied)"
test "$status" = "404" || {
  printf '%s\n' "TLS smoke failed: HTTPS /not-proxied returned $status, expected 404"
  exit 1
}

redirect="$(curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' http://localhost:8080/healthz)"
test "$redirect" = "308 https://localhost:8443/healthz" || {
  printf '%s\n' "TLS smoke failed: HTTP /healthz redirect was '$redirect', expected '308 https://localhost:8443/healthz'"
  exit 1
}

BASE_URL=https://localhost:8443 CURL_CA_BUNDLE="$ca_bundle" ./demo/smoke.sh
