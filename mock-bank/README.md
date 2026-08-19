# Mock Bank

Third-party demo infrastructure bundled with this repository. It simulates a card-issuing
bank so the Payment Gateway has something realistic to fail against. See
[PROVENANCE.md](PROVENANCE.md) for its origin and licensing.

This directory is **not** part of the Payment Gateway implementation. The gateway lives in
[`gateway/`](../gateway/README.md). For the service's own documentation, see
[`bank/README.md`](bank/README.md).

## Mock Bank API

Full API documentation: **<http://localhost:8787/docs>** (Swagger UI)

### Quick Reference

| Operation | Endpoint | Purpose |
|-----------|----------|---------|
| Authorize | `POST /api/v1/authorizations` | Reserve funds on card |
| Capture | `POST /api/v1/captures` | Charge previously authorized funds |
| Void | `POST /api/v1/voids` | Cancel authorization before capture |
| Refund | `POST /api/v1/refunds` | Return money after capture |

All POST endpoints require an `Idempotency-Key` header.

### Test Cards

| Card Number | CVV | Expiry | Balance | Use Case |
|-------------|-----|--------|---------|----------|
| 4111111111111111 | 123 | 12/2030 | $10,000 | Happy path testing |
| 4242424242424242 | 456 | 06/2030 | $500 | Limited balance |
| 5555555555554444 | 789 | 09/2030 | $0 | Insufficient funds |
| 5105105105105100 | 321 | 03/2020 | $5,000 | Expired card |

### Bank Behavior

The mock bank simulates real-world conditions:

- **Amounts in cents**: All monetary values are integers in cents (e.g., `5000` = $50.00)
- **Validation**: Luhn algorithm for card numbers, CVV matching, expiry checks
- **State enforcement**: Can't capture a voided auth, can't void after capture, etc.
- **Idempotency**: Same key + path returns cached response with `X-Idempotent-Replayed: true`
- **Chaos**: ~5% random 500 errors, 100-2000ms latency per request
- **Expiration**: Authorizations expire after 7 days

The chaos behavior is configurable via environment variables for deterministic testing. The
root Compose stack raises the failure rate to `0.10` to exercise the gateway's retry paths.

## Running the Mock Bank standalone

The root `make up` already starts the Mock Bank alongside the gateway. These commands run it
on its own, and do not interact with the root stack.

### With Make (macOS/Linux)

```bash
cd bank && make up      # start
cd bank && make down    # stop
cd bank && make reset   # wipe all data and restart
cd bank && make test    # run the bank's own tests
```

### Without Make (Windows)

```bash
cd docker
docker compose up --build     # start
docker compose down           # stop
docker compose down -v && docker compose up --build   # reset
docker compose exec bank-api go test ./...            # test
```

### What's Running

- PostgreSQL on port 5432
- Bank API on port 8787
- Swagger docs at <http://localhost:8787/docs>
