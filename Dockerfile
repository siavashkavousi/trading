FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o /app/trader \
    ./cmd/trader/

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /app/trader /trader
COPY --from=builder /app/configs /configs

USER nonroot:nonroot

EXPOSE 9090

ENTRYPOINT ["/trader"]
CMD ["--config", "/configs/config.yaml"]
