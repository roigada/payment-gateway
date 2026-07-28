FROM golang:1.25.4-alpine3.21

ENV AIR_VERSION=v1.63.0 \
    GOLANGCI_LINT_VERSION=2.12.2 \
    MIGRATE_VERSION=v4.18.1 \
    MOCKERY_VERSION=v2.53.5

RUN apk add --no-cache \
    git \
    make \
    curl \
    postgresql-client \
    netcat-openbsd

RUN go install github.com/air-verse/air@${AIR_VERSION}

RUN go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@${MIGRATE_VERSION}

RUN go install github.com/vektra/mockery/v2@${MOCKERY_VERSION}

RUN go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}

WORKDIR /app

COPY bank/go.mod bank/go.sum ./
RUN go mod download

COPY bank/ .
COPY docker/bank-api/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

EXPOSE 8080

CMD ["air", "-c", ".air.toml"]
