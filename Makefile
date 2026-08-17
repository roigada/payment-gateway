.PHONY: help up down logs reset ps

COMPOSE := docker compose -f compose.yaml

help: ## Show available commands
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

up: ## Build and start the full local stack in the background
	@$(COMPOSE) up --build -d

down: ## Stop the full local stack while keeping its data volumes
	@$(COMPOSE) down --remove-orphans

logs: ## Stream logs from every service in the local stack
	@$(COMPOSE) logs -f

ps: ## Show the local stack's service status
	@$(COMPOSE) ps

reset: ## Remove the full local stack and all of its volumes, then start clean
	@$(COMPOSE) down --volumes --remove-orphans
	@$(COMPOSE) up --build -d

.DEFAULT_GOAL := help
