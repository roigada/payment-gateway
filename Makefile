.PHONY: demo demo-down demo-reset test

demo:
	docker compose up --build

demo-down:
	docker compose down

demo-reset:
	docker compose down -v

test:
	cd gateway && go test ./...
