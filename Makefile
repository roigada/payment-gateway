.PHONY: demo demo-down demo-reset demo-smoke demo-tls demo-tls-down demo-tls-reset demo-tls-smoke image-smoke observability-check test validate-openapi

TLS_COMPOSE = docker compose -f compose.yaml -f compose.tls-demo.yaml

demo:
	./scripts/ensure-demo-credentials.sh
	docker compose up --build

demo-down:
	docker compose down

demo-reset:
	docker compose down -v

demo-smoke:
	./scripts/ensure-demo-credentials.sh
	if [ -z "$$ORDER_SERVICE_CREDENTIAL" ]; then set -a; . ./.env; set +a; fi; ./demo/smoke.sh

demo-tls:
	./scripts/ensure-demo-credentials.sh
	$(TLS_COMPOSE) up --build

demo-tls-down:
	$(TLS_COMPOSE) down

demo-tls-reset:
	$(TLS_COMPOSE) down -v

demo-tls-smoke:
	./scripts/ensure-demo-credentials.sh
	if [ -z "$$ORDER_SERVICE_CREDENTIAL" ]; then set -a; . ./.env; set +a; fi; ./demo/tls-smoke.sh

image-smoke:
	./scripts/ensure-demo-credentials.sh
	if [ -z "$$ORDER_SERVICE_CREDENTIAL" ]; then set -a; . ./.env; set +a; fi; ./scripts/image-smoke.sh

observability-check:
	jq -e 'any(.panels[]; .title == "Rate-limit rejections by route class" and any(.targets[]; .expr == "sum by (route_class) (rate(payment_gateway_rate_limit_rejections_total[1m]))"))' observability/grafana/dashboards/gateway-overview.json
	docker compose run --rm --no-deps --entrypoint promtool prometheus check config /etc/prometheus/prometheus.yml

test:
	cd gateway && go test ./...

validate-openapi:
	cd gateway && go run ./cmd/openapi-validator
