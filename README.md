# Payment Gateway local stack

Run the complete local environment from the repository root:

```sh
make up
```

This builds and starts the Payment Gateway, its Postgres database and migrations, the Mock Bank and its Postgres database and migrations, Prometheus, and Grafana. The stack is additive: it does not change or use the standalone setup in `mock-bank/`.

## Local endpoints

| Service | Address |
| --- | --- |
| Payment Gateway API | <http://localhost:8080> |
| Payment Gateway metrics | <http://localhost:9091/metrics> |
| Mock Bank API and docs | <http://localhost:8787> |
| Prometheus | <http://localhost:9090> |
| Grafana | <http://localhost:3000> (`admin` / `admin`) |

The root stack configures the Mock Bank with a `0.10` failure rate, so approximately 10% of requests intentionally receive a 500 response. This is for exercising the gateway's retry and recovery behavior.

## Calling the gateway

The local stack includes one intentionally fake, development-only Order Service credential. It grants both read and write scopes:

```sh
curl -i \
  -H 'Authorization: Bearer local-order-service-token' \
  http://localhost:8080/api/v1/payments
```

It is not a production secret. Production credentials and their gateway configuration must be supplied outside this Compose setup.

## Commands

```sh
make up       # build and start in the background
make ps       # show service status
make logs     # stream all service logs
make down     # stop containers, preserving local data
make reset    # delete stack containers, network, and volumes; then start clean
```

`make reset` affects only resources created by this root Compose project. It does not touch the standalone Mock Bank stack or its files.
