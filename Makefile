.PHONY: demo demo-down demo-reset demo-smoke observability-check test

demo:
	docker compose up --build

demo-down:
	docker compose down

demo-reset:
	docker compose down -v

demo-smoke:
	./demo/smoke.sh

observability-check:
	docker compose run --rm --no-deps --entrypoint promtool prometheus check config /etc/prometheus/prometheus.yml

test:
	cd gateway && go test ./...
