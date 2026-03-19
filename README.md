# Crypto Arbitrage Trading System (V1)

A low-latency, event-driven cryptocurrency arbitrage trading system written in Go. It detects and exploits pricing inefficiencies across spot and perpetual futures markets on Nobitex and KCEX, targeting triangular arbitrage and cross-market basis arbitrage strategies for BTC, ETH, and SOL.

See [`docs/architecture.md`](docs/architecture.md) for the full architectural specification.

## Project Layout

```
trading/
├── cmd/trader/main.go              # Application entry point
├── internal/
│   ├── config/                      # Configuration loading, validation, hot-reload
│   ├── domain/                      # Shared types, enums, fixed-point arithmetic
│   ├── marketdata/                  # Order book maintenance, staleness detection
│   ├── strategy/                    # Triangular arb & basis arb signal detection
│   ├── risk/                        # Risk limits, kill switch, daily PnL tracking
│   ├── execution/                   # Multi-leg order sequencing, retries, timeouts
│   ├── order/                       # Order lifecycle, idempotency, UUIDv7 IDs
│   ├── costmodel/                   # Fee tiers, slippage curves, funding estimation
│   ├── portfolio/                   # Position tracking, venue reconciliation
│   ├── gateway/                     # Venue adapters (Nobitex, KCEX, simulated)
│   ├── eventbus/                    # In-process typed pub/sub
│   ├── persistence/                 # SQLite checkpoints, PostgreSQL cold store
│   └── monitor/                     # Prometheus metrics, tracing, alerts
├── configs/config.yaml              # Default configuration
├── migrations/                      # PostgreSQL migration files
├── scripts/                         # Docker Compose, Prometheus config, Makefile
└── Dockerfile                       # Multi-stage build → distroless image
```

## Prerequisites

### Native

- **Go 1.22+** — [https://go.dev/dl/](https://go.dev/dl/)
- **PostgreSQL 16+** (optional) — only needed if you configure a `cold_store_dsn` for persistent trade history. The system runs fine without it; SQLite handles risk checkpoints locally.

### Docker

- **Docker Engine 20.10+**
- **Docker Compose v2** (bundled with modern Docker Desktop)

## Configuration

All runtime behavior is controlled through a single YAML file. The default config ships at `configs/config.yaml` and starts the system in **dry-run mode** (paper trading against live market data, no real orders).

### Trading Modes

| Mode | Config value | Description |
|------|-------------|-------------|
| **Dry run** | `dry_run` | Simulates order execution locally against live market data. No real orders are sent. Default mode. |
| **Live** | `live` | Places real orders on exchanges. Requires the `--confirm-live` CLI flag and valid API keys. |
| **Backtest** | `backtest` | Replays historical data (post-V1). |

### Environment Variables

API keys are **never** stored in config files. They are injected via environment variables:

```bash
# Nobitex
export NOBITEX_API_KEY="your-api-key"
export NOBITEX_API_SECRET="your-api-secret"

# KCEX
export KCEX_API_KEY="your-api-key"
export KCEX_API_SECRET="your-api-secret"

# PostgreSQL (only if using cold store)
export POSTGRES_PASSWORD="your-db-password"
```

In dry-run mode, trading API keys are not required — only market data feeds need access.

## Running Natively

### 1. Clone and install dependencies

```bash
git clone https://github.com/siavashkavousi/trading.git
cd trading
go mod download
```

### 2. Create the data directory

The system stores SQLite checkpoints locally:

```bash
mkdir -p data
```

### 3. Build

```bash
CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/trader ./cmd/trader/
```

Or using the Makefile:

```bash
make -f scripts/Makefile build
```

### 4. Run in dry-run mode (default)

```bash
./bin/trader --config configs/config.yaml
```

Or with `go run`:

```bash
go run ./cmd/trader/ --config configs/config.yaml
```

The system starts in `dry_run` mode, connects to venue WebSocket feeds for live market data, and simulates order execution locally. No real orders are placed.

### 5. Run in live mode

First, set your API keys as environment variables (see above), then set `trading_mode: "live"` in `configs/config.yaml` and run:

```bash
./bin/trader --config configs/config.yaml --confirm-live
```

The `--confirm-live` flag is a safety guard that prevents accidentally running in live mode.

### 6. Run tests

```bash
go test ./... -race -v
```

Or:

```bash
make -f scripts/Makefile test
```

### 7. Metrics endpoint

Once running, Prometheus metrics are available at:

```
http://localhost:9090/metrics
```

A health check endpoint is also exposed:

```
http://localhost:9090/health
```

## Running with Docker

### Option A: Standalone container

Build the image (produces a ~15 MB distroless container with just the static binary):

```bash
docker build -t crypto-trader:latest .
```

Run in dry-run mode:

```bash
docker run -p 9090:9090 \
  -v $(pwd)/configs:/configs:ro \
  -v $(pwd)/data:/data \
  crypto-trader:latest
```

Run in live mode with API keys:

```bash
docker run -p 9090:9090 \
  -e NOBITEX_API_KEY="your-key" \
  -e NOBITEX_API_SECRET="your-secret" \
  -e KCEX_API_KEY="your-key" \
  -e KCEX_API_SECRET="your-secret" \
  -v $(pwd)/configs:/configs:ro \
  -v $(pwd)/data:/data \
  crypto-trader:latest --config /configs/config.yaml --confirm-live
```

### Option B: Full stack with Docker Compose

Docker Compose brings up the trader alongside PostgreSQL, Prometheus, and Grafana:

```bash
cd scripts
docker compose up -d
```

This starts four services:

| Service | Port | Description |
|---------|------|-------------|
| **trader** | `9090` | The trading system (metrics at `/metrics`, health at `/health`) |
| **postgres** | `5432` | PostgreSQL 16 for trade history cold storage |
| **prometheus** | `9091` | Prometheus scraping the trader's `/metrics` endpoint every 10s |
| **grafana** | `3000` | Dashboards (default login: `admin` / `admin`) |

To pass API keys, create a `.env` file in the `scripts/` directory:

```bash
# scripts/.env
NOBITEX_API_KEY=your-key
NOBITEX_API_SECRET=your-secret
KCEX_API_KEY=your-key
KCEX_API_SECRET=your-secret
POSTGRES_PASSWORD=your-secure-password
GRAFANA_PASSWORD=your-grafana-password
```

To use the PostgreSQL cold store, update `configs/config.yaml`:

```yaml
persistence:
  cold_store_dsn: "postgres://trader:your-secure-password@postgres:5432/trading?sslmode=disable"
```

Stop the stack:

```bash
docker compose down
```

Stop and remove all data volumes:

```bash
docker compose down -v
```

## CLI Reference

```
Usage: trader [flags]

Flags:
  --config string       Path to configuration file (default "configs/config.yaml")
  --confirm-live        Required safety flag to run in live trading mode
```

## Makefile Targets

Run these from the project root with `make -f scripts/Makefile <target>`:

| Target | Description |
|--------|-------------|
| `build` | Compile static binary to `bin/trader` |
| `test` | Run all tests with race detection |
| `lint` | Run `golangci-lint` |
| `run` | Build and run with default config |
| `run-dry` | `go run` in dry-run mode |
| `run-live` | `go run` in live mode (needs `--confirm-live`) |
| `clean` | Remove build artifacts and local DB |
| `docker` | Build the Docker image |
| `tidy` | Run `go mod tidy` |
| `fmt` | Format all Go source files |
| `vet` | Run `go vet` |

## Transitioning from Dry Run to Live

The recommended promotion workflow:

1. Deploy in `dry_run` mode with full production configuration.
2. Run for at least 72 hours, covering multiple funding intervals and market regimes.
3. Review dry-run performance — realized edge, slippage model accuracy, risk limit behavior, alert correctness, latency.
4. If satisfactory: reduce risk limits to 25% of production values, switch to `live` mode with `--confirm-live`, monitor the first 4 hours closely, then gradually increase limits over 48 hours.
5. If unsatisfactory: tune parameters and repeat from step 2.

## Key Operational Notes

- **Kill switch**: Triggered automatically on daily PnL breach (-12,500 USDT) or manually. Cancels all orders, flattens exposure, and persists across restarts. Requires manual deactivation to resume.
- **Graceful shutdown**: `SIGINT` / `SIGTERM` cancels all open orders before exiting.
- **Hot reload**: Strategy parameters, risk limits (tighter only), and cost model settings can be updated by editing the config file while the system is running. Venue settings and system tuning require a restart.
- **Reconciliation**: Every 60 seconds the system queries venue APIs to verify internal position/balance state. Mismatches above 0.5% trigger a P1 alert and block trading for the affected venue.
