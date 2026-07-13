#!/bin/sh
set -eu

project="payment-gateway-image-smoke"
compose="docker compose -p $project -f compose.yaml -f compose.image-smoke.yaml"
gateway_port="${IMAGE_SMOKE_GATEWAY_PORT:-18080}"
mock_bank_port="${IMAGE_SMOKE_MOCK_BANK_PORT:-18787}"

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    $compose logs --no-color postgres migrate mock-bank-postgres mock-bank payment-gateway || true
  fi
  $compose down --volumes --remove-orphans
  trap - EXIT INT TERM
  exit "$status"
}

trap cleanup EXIT

# A clean, project-scoped volume ensures this path never reuses demo data.
$compose down --volumes --remove-orphans
$compose up --build --detach postgres migrate mock-bank-postgres mock-bank payment-gateway

test "$(docker inspect --format='{{.State.ExitCode}}' "$($compose ps -a -q migrate)")" = "0"
docker compose -p "$project" -f compose.yaml -f compose.image-smoke.yaml exec -T postgres pg_isready -U payment_gateway -d payment_gateway
curl --fail --show-error --silent "http://127.0.0.1:$mock_bank_port/health" >/dev/null

BASE_URL="http://127.0.0.1:$gateway_port" \
MOCK_BANK_BASE_URL="http://127.0.0.1:$mock_bank_port" \
READY_TIMEOUT_SECONDS="180" \
./demo/smoke.sh
