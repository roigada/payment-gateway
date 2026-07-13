# syntax=docker/dockerfile:1

FROM golang:1.26.1-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/payment-gateway ./cmd/payment-gateway

FROM gcr.io/distroless/static-debian12:nonroot

COPY --chown=nonroot:nonroot --from=build /out/payment-gateway /payment-gateway
COPY --chown=nonroot:nonroot --from=build /src/migrations /migrations

USER nonroot:nonroot

ENTRYPOINT ["/payment-gateway"]
