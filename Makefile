.PHONY: demo demo-down demo-reset demo-smoke image-smoke observability-check test validate-openapi

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

image-smoke:
	./scripts/ensure-demo-credentials.sh
	if [ -z "$$ORDER_SERVICE_CREDENTIAL" ]; then set -a; . ./.env; set +a; fi; ./scripts/image-smoke.sh

observability-check:
	docker compose run --rm --no-deps --entrypoint promtool prometheus check config /etc/prometheus/prometheus.yml

test:
	cd gateway && go test ./...

validate-openapi:
	cd gateway && go run ./cmd/openapi-validator
