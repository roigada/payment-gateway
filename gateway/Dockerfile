FROM golang:1.26.1-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /payment-gateway ./cmd/payment-gateway

FROM alpine:3.22

RUN adduser -D -H -u 10001 gateway

USER gateway
COPY --from=build /payment-gateway /payment-gateway

EXPOSE 8080 9091

ENTRYPOINT ["/payment-gateway"]
