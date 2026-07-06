.PHONY: demo demo-down demo-reset demo-smoke test

demo:
	docker compose up --build

demo-down:
	docker compose down

demo-reset:
	docker compose down -v

demo-smoke:
	./demo/smoke.sh

test:
	cd gateway && go test ./...
