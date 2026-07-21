#!/bin/sh
set -eu

BASE_URL="${BASE_URL:-http://localhost:8080}"
MOCK_BANK_BASE_URL="${MOCK_BANK_BASE_URL:-}"
READY_TIMEOUT_SECONDS="${READY_TIMEOUT_SECONDS:-180}"
ORDER_SERVICE_CREDENTIAL="${ORDER_SERVICE_CREDENTIAL:?ORDER_SERVICE_CREDENTIAL is required}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

log() {
  printf '%s\n' "$*"
}

fail() {
  log "smoke failed: $*"
  exit 1
}

request() {
  method="$1"
  path="$2"
  body_file="$3"
  response_file="$4"
  shift 4

  status_file="$tmpdir/status"
  if [ "$body_file" = "-" ]; then
    status="$(curl -sS -o "$response_file" -w '%{http_code}' -X "$method" "$BASE_URL$path" "$@")" || {
      cat "$response_file" 2>/dev/null || true
      fail "$method $path could not reach gateway"
    }
  else
    status="$(curl -sS -o "$response_file" -w '%{http_code}' -X "$method" "$BASE_URL$path" "$@" --data-binary "@$body_file")" || {
      cat "$response_file" 2>/dev/null || true
      fail "$method $path could not reach gateway"
    }
  fi

  printf '%s' "$status" > "$status_file"
}

expect_status() {
  expected="$1"
  actual="$(cat "$tmpdir/status")"
  response_file="$2"
  context="$3"

  if [ "$actual" != "$expected" ]; then
    log "$context returned HTTP $actual, expected $expected"
    log "response body:"
    cat "$response_file"
    exit 1
  fi
}

extract_payment_id() {
  sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$1" | head -n 1
}

extract_payment_status() {
  sed -n 's/.*"status":"\([^"]*\)".*/\1/p' "$1" | head -n 1
}

log "waiting for gateway readiness at $BASE_URL/readyz"
deadline=$(( $(date +%s) + READY_TIMEOUT_SECONDS ))
ready_body="$tmpdir/ready.json"
while :; do
  if status="$(curl -sS -o "$ready_body" -w '%{http_code}' "$BASE_URL/readyz" 2>/dev/null)" && [ "${status#2}" != "$status" ]; then
    break
  fi

  if [ "$(date +%s)" -ge "$deadline" ]; then
    log "last readiness response:"
    cat "$ready_body" 2>/dev/null || true
    fail "gateway did not become ready within ${READY_TIMEOUT_SECONDS}s"
  fi

  sleep 1
done

if [ -n "$MOCK_BANK_BASE_URL" ]; then
  log "checking Mock Bank health at $MOCK_BANK_BASE_URL/health"
  mock_bank_body="$tmpdir/mock-bank-health.json"
  if ! status="$(curl -sS -o "$mock_bank_body" -w '%{http_code}' "$MOCK_BANK_BASE_URL/health")" || [ "${status#2}" = "$status" ]; then
    log "last Mock Bank health response:"
    cat "$mock_bank_body" 2>/dev/null || true
    fail "Mock Bank health returned HTTP ${status:-unreachable}"
  fi
fi

authorize_body="$tmpdir/authorize.json"
cat > "$authorize_body" <<'JSON'
{
  "order_id": "demo-smoke-order",
  "customer_id": "demo-smoke-customer",
  "amount": 1299,
  "card": {
    "number": "4111111111111111",
    "cvv": "123",
    "expiry_month": 12,
    "expiry_year": 2030
  }
}
JSON

authorize_response="$tmpdir/authorize-response.json"
request POST /v1/payments "$authorize_body" "$authorize_response" \
  -H "Authorization: Bearer $ORDER_SERVICE_CREDENTIAL" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: demo-smoke-authorize-$(date +%s)"
expect_status 201 "$authorize_response" "authorize Payment"

payment_id="$(extract_payment_id "$authorize_response")"
if [ -z "$payment_id" ]; then
  log "authorize response body:"
  cat "$authorize_response"
  fail "authorize response did not include a Payment ID"
fi

capture_response="$tmpdir/capture-response.json"
request POST "/v1/payments/$payment_id/capture" - "$capture_response" \
  -H "Authorization: Bearer $ORDER_SERVICE_CREDENTIAL" \
  -H "Idempotency-Key: demo-smoke-capture-$(date +%s)"
expect_status 200 "$capture_response" "capture Payment"

fetch_response="$tmpdir/fetch-response.json"
request GET "/v1/payments/$payment_id" - "$fetch_response" \
  -H "Authorization: Bearer $ORDER_SERVICE_CREDENTIAL"
expect_status 200 "$fetch_response" "fetch Payment"

status="$(extract_payment_status "$fetch_response")"
if [ "$status" != "captured" ]; then
  log "fetched Payment response body:"
  cat "$fetch_response"
  fail "expected fetched Payment Status captured, got ${status:-empty}"
fi

log "smoke passed: Payment $payment_id is captured"
