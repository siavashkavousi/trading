# Architecture Document — Crypto Arbitrage Trading System (V1)

> **Version**: 1.0  
> **Last updated**: 2026-02-23  
> **Status**: Draft — V1 Scope

---

## Table of Contents

1. [Introduction](#1-introduction)
2. [System Overview](#2-system-overview)
3. [Design Principles](#3-design-principles)
4. [High-Level Architecture](#4-high-level-architecture)
5. [Component Breakdown](#5-component-breakdown)
   - 5.1 [Market Data Service](#51-market-data-service)
   - 5.2 [Strategy Engine](#52-strategy-engine)
   - 5.3 [Execution Engine](#53-execution-engine)
   - 5.4 [Risk Manager](#54-risk-manager)
   - 5.5 [Cost Model Service](#55-cost-model-service)
   - 5.6 [Position & Portfolio Manager](#56-position--portfolio-manager)
   - 5.7 [Order Manager](#57-order-manager)
   - 5.8 [Venue Gateway Layer](#58-venue-gateway-layer)
   - 5.9 [Monitoring & Observability](#59-monitoring--observability)
   - 5.10 [Configuration Service](#510-configuration-service)
   - 5.11 [Persistence Layer](#511-persistence-layer)
6. [Data Flow](#6-data-flow)
   - 6.1 [Triangular Arbitrage Flow](#61-triangular-arbitrage-flow)
   - 6.2 [Cross-Market Basis Arbitrage Flow](#62-cross-market-basis-arbitrage-flow)
7. [Venue Integration](#7-venue-integration)
8. [Risk Management Architecture](#8-risk-management-architecture)
9. [Latency Architecture](#9-latency-architecture)
10. [Deployment Architecture](#10-deployment-architecture)
11. [Security Architecture](#11-security-architecture)
12. [Technology Stack](#12-technology-stack)
13. [Project Layout](#13-project-layout)
14. [Data Model](#14-data-model)
15. [Dry Run / Paper Trading Mode](#15-dry-run--paper-trading-mode)
16. [Failure Modes & Recovery](#16-failure-modes--recovery)
17. [Future Considerations (Post-V1)](#17-future-considerations-post-v1)

---

## 1. Introduction

This document defines the software architecture for a cryptocurrency arbitrage trading system. The system is designed to detect and exploit pricing inefficiencies across spot and perpetual futures markets on supported exchanges. V1 targets two strategies — **triangular arbitrage** and **cross-market basis arbitrage** — operating on **Nobitex** and **KCEX** with a focused symbol universe of BTC, ETH, and SOL.

### 1.1 Purpose

- Serve as the single source of truth for architectural decisions.
- Guide implementation teams on component boundaries, data flows, and integration contracts.
- Establish non-functional requirements (latency, uptime, observability) as first-class architectural constraints.

### 1.2 Scope

This document covers the V1 system only. Spatial (cross-venue) arbitrage, additional exchanges, and expanded asset coverage are explicitly out of scope but are considered in extensibility decisions.

### 1.3 References

| Document | Location |
|---|---|
| Requirements & Strategy Specification | `docs/requirements.md` |

---

## 2. System Overview

The system operates as a low-latency event-driven pipeline:

```
Market Data → Signal Detection → Risk Check → Order Generation → Execution → Settlement
```

**Key characteristics:**

- **Event-driven**: All components communicate via an internal event bus; processing is triggered by market data ticks, order state changes, and risk events.
- **Single-venue per strategy cycle**: V1 strategies operate within a single venue per execution cycle, avoiding cross-venue transfer latency.
- **Latency-sensitive**: The end-to-end tick-to-acknowledgement budget is 180 ms (p95), requiring careful architectural choices around serialization, memory allocation, and network hops.
- **Risk-first**: Every order must pass through the Risk Manager before reaching the venue. There is no bypass path.

---

## 3. Design Principles

| Principle | Rationale |
|---|---|
| **Risk gates are non-bypassable** | All order flow passes through the Risk Manager synchronously. No shortcut paths exist, even under degraded conditions. |
| **Fail-safe over fail-open** | On ambiguity (stale data, unclear position state, risk limit uncertainty), the system halts new order placement and flattens exposure. |
| **Hot-path isolation** | The critical path (market data → decision → order) is kept free of disk I/O and shared locks. Go's sub-millisecond GC pauses are acceptable, but allocation pressure on the hot path is minimized through object pooling and pre-allocated buffers. Logging, persistence, and analytics are handled asynchronously on separate goroutines. |
| **Venue abstraction** | All venue-specific protocol details are encapsulated behind a unified gateway interface, enabling exchange addition without modifying strategy or risk logic. |
| **Idempotent operations** | Order submissions, cancellations, and state transitions are idempotent to handle retries safely in the presence of network instability. |
| **Observable by default** | Every component emits structured metrics, traces, and logs. Silent failures are treated as architecture bugs. |

### 3.1 Why Not Formal Hexagonal Architecture?

The system uses **interface-based boundaries** at integration points rather than a formal hexagonal (ports & adapters) architecture. This is a deliberate choice:

| Hexagonal concept | What we do instead | Rationale |
|---|---|---|
| Explicit `ports/` package with `inbound`/`outbound` sub-packages | Interfaces defined alongside the package that owns the contract (e.g., `gateway.VenueGateway` in `internal/gateway/`) | Avoids a package that exists only to hold interface declarations, reducing navigational overhead in a single-binary app. |
| Application services layer between domain and ports | Strategy Engine, Risk Manager, and Execution Engine call interfaces directly | The hot path (signal → risk → execute) must be as direct as possible. An extra layer adds call depth and complicates latency tracing for negligible abstraction benefit. |
| Domain services vs. application services | Single `internal/domain/` package for shared types; business logic lives in purpose-named packages (`strategy/`, `risk/`, `execution/`) | The domain is narrow (arbitrage detection, risk, order management). Further subdivision fragments code that developers need to read together. |
| Framework-managed dependency injection | Manual wiring in `cmd/trader/main.go` | Go's implicit interface satisfaction provides decoupling without a DI container. The composition root is ~100 lines and easily auditable. |

**What we keep from hexagonal:** the core insight that external dependencies (venues, databases, metrics backends) must sit behind interfaces so they can be swapped (real ↔ simulated for dry run, real ↔ mock for tests) without touching business logic. The `VenueGateway` interface with its Nobitex, KCEX, and Simulated adapters is this pattern in practice.

**Why this fits Go:** The Go community favors flat package structures, concrete-first design (define an interface when you need a second implementation, not before), and minimal indirection. Formal hexagonal in Go codebases often results in single-file packages and redundant type aliases that add ceremony without clarity.

---

## 4. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           TRADING SYSTEM (V1)                               │
│                                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐                   │
│  │   Nobitex     │    │    KCEX      │    │  External    │                   │
│  │   Exchange    │    │   Exchange   │    │  Price Feeds │                   │
│  └──────┬───────┘    └──────┬───────┘    └──────┬───────┘                   │
│         │ WS/REST           │ WS/REST           │                           │
│  ┌──────▼───────────────────▼───────────────────▼───────┐                   │
│  │              VENUE GATEWAY LAYER                      │                   │
│  │  ┌─────────────────┐  ┌─────────────────┐            │                   │
│  │  │ Nobitex Gateway  │  │  KCEX Gateway   │            │                   │
│  │  │ (Adapter)        │  │  (Adapter)      │            │                   │
│  │  └────────┬─────────┘  └────────┬────────┘            │                   │
│  └───────────┼─────────────────────┼─────────────────────┘                   │
│              │ Normalized Events   │                                         │
│  ┌───────────▼─────────────────────▼─────────────────────┐                   │
│  │              INTERNAL EVENT BUS                        │                   │
│  │   (In-process pub/sub, lock-free ring buffer)         │                   │
│  └──┬──────────┬──────────┬──────────┬──────────┬────────┘                   │
│     │          │          │          │          │                             │
│  ┌──▼──┐  ┌───▼───┐  ┌──▼───┐  ┌──▼──────┐ ┌▼─────────┐                   │
│  │Market│  │Strategy│  │Risk  │  │Execution│ │Position & │                   │
│  │Data  │  │Engine  │  │Mgr   │  │Engine   │ │Portfolio  │                   │
│  │Svc   │  │        │  │      │  │         │ │Manager    │                   │
│  └──────┘  └────────┘  └──────┘  └─────────┘ └───────────┘                   │
│     │          │          │          │          │                             │
│  ┌──▼──────────▼──────────▼──────────▼──────────▼────────┐                   │
│  │           SUPPORTING SERVICES                          │                   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ │                   │
│  │  │Cost Model│ │Order Mgr │ │Config Svc│ │Persist.  │ │                   │
│  │  │Service   │ │          │ │          │ │Layer     │ │                   │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘ │                   │
│  └───────────────────────────────────────────────────────┘                   │
│                                                                             │
│  ┌──────────────────────────────────────────────────────┐                    │
│  │          MONITORING & OBSERVABILITY                   │                    │
│  │  Metrics │ Traces │ Logs │ Alerts │ Dashboards       │                    │
│  └──────────────────────────────────────────────────────┘                    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 5. Component Breakdown

### 5.1 Market Data Service

**Responsibility**: Maintain a real-time, consistent view of order book state and trade data for all watched symbols across all venues.

**Inputs**:
- WebSocket streams from venue gateways (order book deltas, trades, funding rates)
- REST polling fallback for venues with unreliable WebSocket delivery

**Outputs**:
- Normalized `OrderBookSnapshot` events (top-N levels, configurable depth)
- Normalized `Trade` tick events
- `FundingRate` update events (for perp instruments)
- `DataStaleness` alert events when freshness SLA is breached

**Key design decisions**:
- Maintains an **in-memory order book** per symbol/venue, updated incrementally from deltas.
- Assigns a **sequence number and receive timestamp** to every update for staleness detection.
- Publishes a **heartbeat** every 500 ms per feed; downstream consumers treat missed heartbeats as a staleness signal.
- Freshness SLA: data older than **500 ms** is flagged stale; data older than **2 seconds** triggers execution blocking.

**Internal data structures**:
- Price-level sorted slices (bid descending, ask ascending) for O(1) best-bid/ask access, backed by pre-allocated arrays to avoid GC pressure.
- Lock-free ring buffer (implemented via `sync/atomic`) for recent trade ticks (last 1000 per symbol).

---

### 5.2 Strategy Engine

**Responsibility**: Detect arbitrage opportunities and generate candidate trade signals.

The Strategy Engine hosts independent strategy modules that subscribe to market data events and emit `TradeSignal` objects when profitable opportunities are detected.

#### 5.2.1 Triangular Arbitrage Module

**Logic**: Continuously evaluate all valid triangular paths across the three core assets (BTC, ETH, SOL) quoted in USDT on a single venue.

A triangular path example on a venue:
```
USDT → BTC (buy BTC/USDT) → ETH (sell BTC/ETH or equivalent) → USDT (sell ETH/USDT)
```

**Detection algorithm**:
1. On any order book update for a watched symbol, recompute the implied cross rate for all triangular paths involving that symbol.
2. Compare the implied cross rate against the direct market rate.
3. If the discrepancy exceeds the **minimum threshold (18 bps)** after modeled costs, emit a `TriArbSignal` with:
   - Direction (clockwise / counterclockwise path)
   - Venue
   - Leg details (symbol, side, price, quantity)
   - Expected gross edge (bps)
   - Modeled cost breakdown
   - Confidence score
   - Signal timestamp

**Sizing logic**:
- Size each leg to the **minimum available liquidity** across all three legs at the quoted prices, capped by per-asset and per-venue risk limits.
- Apply a **fill-probability discount** based on historical fill rates at each price level.

#### 5.2.2 Cross-Market Basis Arbitrage Module

**Logic**: Monitor the basis (spot price vs. perp mark price) and funding rate regime for each asset. When the combined expected capture exceeds the threshold (22 bps net), emit a `BasisArbSignal`.

**Detection algorithm**:
1. Compute the **annualized basis** = `(perp_price - spot_price) / spot_price * (365 / holding_horizon_days)`.
2. Estimate **funding capture** over the expected holding horizon using a rolling average of recent funding rates, weighted by recency.
3. Sum basis + funding capture and subtract modeled costs (fees, slippage, funding uncertainty, withdrawal amortization).
4. If the net expected edge >= 22 bps, emit a `BasisArbSignal` with:
   - Asset, venue
   - Spot leg and perp leg details
   - Expected holding horizon
   - Annualized basis at entry
   - Funding rate regime classification (contango/backwardation, stable/volatile)
   - Net expected edge
   - Signal timestamp

**Regime detection**:
- Classify funding regime as **stable** (std dev of 8h funding < 0.01%) or **volatile**.
- Apply wider uncertainty buffers during volatile regimes.

---

### 5.3 Execution Engine

**Responsibility**: Convert validated trade signals into executable order sequences and manage their lifecycle until completion or cancellation.

**Inputs**:
- Risk-approved `TradeSignal` objects from the Risk Manager.

**Outputs**:
- `OrderRequest` messages sent to the Order Manager.
- `ExecutionReport` events published to the event bus.

**Key behaviors**:

| Behavior | Detail |
|---|---|
| **Atomic leg submission** | For triangular arb, all three legs are submitted in rapid sequence (target < 10 ms between legs). If any leg fails pre-flight checks, the entire cycle is aborted. |
| **Partial fill handling** | If a leg partially fills, the Execution Engine adjusts subsequent leg sizes proportionally and may place a hedge order to neutralize residual exposure. |
| **Timeout management** | Each leg has a configurable fill timeout (default: 3 seconds for tri-arb, 15 seconds for basis arb). Unfilled orders are cancelled on timeout. |
| **Retry policy** | Transient venue errors (rate limit, temporary unavailability) trigger up to 2 retries with 50 ms backoff. Persistent errors cancel the cycle. |
| **Execution quality tracking** | Every fill is compared against the signal's expected price to compute realized slippage. |

**Execution modes**:
- **Aggressive (taker)**: Market or limit-at-best orders for time-sensitive triangular arb.
- **Passive (maker)**: Limit orders posted inside the spread for basis arb entry, where latency is less critical.
- **Dry run (paper)**: Orders are simulated locally instead of being sent to the venue. See [Section 15](#15-dry-run--paper-trading-mode) for full details.

---

### 5.4 Risk Manager

**Responsibility**: Enforce all risk limits defined in the requirements. The Risk Manager is the **mandatory gateway** between signal generation and order execution.

**Risk checks (synchronous, blocking)**:

| Check | Limit | Action on breach |
|---|---|---|
| Per-asset net exposure | BTC ≤ 1.5, ETH ≤ 25, SOL ≤ 800 | Reject signal |
| Per-venue gross notional | Nobitex ≤ 250K USDT, KCEX ≤ 200K USDT | Reject signal |
| Daily PnL loss cap | ≤ −12,500 USDT/day | Cancel all orders, flatten, halt trading, require manual resume |
| Global open orders | ≤ 120 | Reject signal until orders drain |
| Per-venue open orders | ≤ 70 | Reject signal for that venue |
| Per-symbol open orders | ≤ 20 | Reject signal for that symbol |
| Market data freshness | Data age ≤ 500 ms | Block execution |

**Architecture**:
- Maintains an **in-memory risk state** that is updated synchronously on every order fill, cancellation, and position change.
- Risk state is **checkpointed** to persistent storage every 5 seconds and on every state transition that crosses 80% of any limit.
- On startup, risk state is reconstructed from the last checkpoint plus venue position queries.

**Kill switch**:
- A dedicated **kill switch** mechanism can be triggered manually (API/CLI) or automatically (daily loss cap breach).
- Kill switch action: cancel all open orders across all venues, close positions to flat/hedged, disable signal processing.
- Kill switch state persists across restarts; manual confirmation required to re-enable.

---

### 5.5 Cost Model Service

**Responsibility**: Provide accurate, up-to-date cost estimates for any proposed trade, enabling the Strategy Engine and Risk Manager to make informed profitability decisions.

**Cost components modeled**:

| Component | Data Source | Update Frequency |
|---|---|---|
| Trading fees (maker/taker) | Venue API fee tier endpoint | On startup + every 1 hour |
| Slippage estimate | Historical fill data + current order book depth | Per-signal (real-time) |
| Funding rate (perp) | Venue funding rate stream | Every funding interval (typically 8h) |
| Withdrawal/transfer fees | Venue API or manual config | On startup + daily |

**Slippage model**:
- Maintains per-symbol, per-venue **slippage curves** as a function of order size.
- Curves are fitted from the last 500 fills using a piecewise linear model.
- For new symbols or insufficient data, a conservative default curve is used.

**Interface**:
```go
type CostEstimate struct {
    FeeBps      decimal.Decimal
    SlippageBps decimal.Decimal
    FundingBps  *decimal.Decimal // nil when not applicable
    TotalBps    decimal.Decimal
    Confidence  decimal.Decimal
}

type CostModelService interface {
    EstimateCost(venue, symbol string, side Side, size decimal.Decimal, orderType OrderType) (CostEstimate, error)
}
```

---

### 5.6 Position & Portfolio Manager

**Responsibility**: Track real-time positions, balances, and portfolio-level PnL across all venues and instruments.

**State maintained**:
- Per-asset, per-venue spot balances (free + locked).
- Per-asset, per-venue perp positions (size, entry price, unrealized PnL, margin).
- Portfolio-level aggregated net exposure per asset.
- Realized and unrealized PnL, tracked daily with UTC midnight reset.

**Reconciliation**:
- Every **60 seconds**, query venue APIs for authoritative balance/position snapshots.
- Compare against internal state; log discrepancies.
- If discrepancy exceeds configurable threshold (default: 0.5% of position value), raise a **P1 alert** and block new trading for the affected venue until resolved.

**PnL calculation**:
- Mark-to-market using the mid-price from the Market Data Service.
- Realized PnL updated on every fill event.
- Daily PnL aggregated for the Risk Manager's daily loss cap check.

---

### 5.7 Order Manager

**Responsibility**: Maintain the authoritative state of all orders in the system and provide a clean interface between the Execution Engine and venue gateways.

**Order lifecycle states**:
```
PENDING_NEW → SUBMITTED → ACKNOWLEDGED → PARTIAL_FILL → FILLED
                                       → CANCELLED
                                       → REJECTED
                    → SUBMIT_FAILED
```

**Key behaviors**:
- Assigns a **unique internal order ID** (UUID v7 for time-ordering) to every order, mapped to the venue's external order ID.
- Maintains an **in-memory order book** of all active orders for fast lookup.
- Publishes `OrderStateChange` events to the event bus on every transition.
- Implements **order deduplication** using idempotency keys to prevent double-submission on retries.
- Tracks order creation timestamps for staleness detection and timeout enforcement.

---

### 5.8 Venue Gateway Layer

**Responsibility**: Encapsulate all venue-specific communication protocols behind a uniform internal interface.

**Per-venue adapter responsibilities**:
- WebSocket connection lifecycle management (connect, authenticate, subscribe, heartbeat, reconnect).
- REST API request management with rate limiting, retry logic, and error code translation.
- Message serialization/deserialization (venue-specific JSON/binary → internal normalized types).
- Sequence number and nonce management for authenticated endpoints.
- Request signing (HMAC or other venue-required schemes).

**Unified gateway interface**:

```go
type VenueGateway interface {
    // Market data — return channels for streaming data
    SubscribeOrderBook(ctx context.Context, symbol string) (<-chan OrderBookDelta, error)
    SubscribeTrades(ctx context.Context, symbol string) (<-chan Trade, error)
    SubscribeFunding(ctx context.Context, symbol string) (<-chan FundingRate, error)

    // Trading
    PlaceOrder(ctx context.Context, req OrderRequest) (*OrderAck, error)
    CancelOrder(ctx context.Context, orderID string) (*CancelAck, error)
    GetOpenOrders(ctx context.Context, symbol string) ([]Order, error)

    // Account
    GetBalances(ctx context.Context) (map[string]Balance, error)
    GetPositions(ctx context.Context) ([]Position, error)
    GetFeeTier(ctx context.Context) (*FeeTier, error)

    // Lifecycle
    Connect(ctx context.Context) error
    Close() error
}
```

**Reconnection policy**:
- On WebSocket disconnect: immediate reconnect with exponential backoff (100 ms, 200 ms, 400 ms, ..., max 30 s).
- On reconnect: re-subscribe to all streams and request a full order book snapshot to resync state.
- After 5 consecutive failures: raise P1 alert and disable trading for that venue.

#### 5.8.1 Nobitex Gateway

- **Market data**: WebSocket for order book and trades; REST fallback for funding data.
- **Trading**: REST API with HMAC-SHA256 authentication.
- **Rate limits**: Enforced client-side with a token bucket; configurable per endpoint category.

#### 5.8.2 KCEX Gateway

- **Market data**: WebSocket for order book, trades, and funding rates.
- **Trading**: REST API with API key + secret signature.
- **Rate limits**: Enforced client-side; separate buckets for public and private endpoints.

---

### 5.9 Monitoring & Observability

**Responsibility**: Provide comprehensive visibility into system health, performance, and trading outcomes.

**Three pillars**:

#### Metrics (Time-Series)

| Metric | Type | Labels |
|---|---|---|
| `md_to_decision_latency_ms` | Histogram | strategy, venue, symbol |
| `decision_to_ack_latency_ms` | Histogram | strategy, venue, symbol |
| `e2e_tick_to_ack_latency_ms` | Histogram | strategy, venue, symbol |
| `realized_edge_bps` | Histogram | strategy, venue |
| `expected_edge_bps` | Histogram | strategy, venue |
| `fill_slippage_bps` | Histogram | venue, symbol, side |
| `funding_paid_received_usdt` | Counter | venue, asset, direction |
| `risk_limit_utilization_pct` | Gauge | limit_type, venue, asset |
| `risk_limit_breach_total` | Counter | limit_type |
| `order_reject_total` | Counter | venue, reason |
| `order_cancel_total` | Counter | venue, reason |
| `open_order_count` | Gauge | venue, symbol |
| `position_net_exposure` | Gauge | asset |
| `daily_pnl_usdt` | Gauge | — |
| `venue_ws_reconnect_total` | Counter | venue |
| `venue_api_error_total` | Counter | venue, endpoint, error_code |

#### Traces (Distributed)

- Every trade signal is assigned a **trace ID** that propagates through Risk Manager → Execution Engine → Order Manager → Venue Gateway.
- Trace spans capture latency at each boundary, enabling latency attribution.

#### Logs (Structured)

- JSON-structured logs with fields: `timestamp`, `level`, `component`, `trace_id`, `message`, `context`.
- Log levels: `DEBUG` (hot-path disabled in production), `INFO`, `WARN`, `ERROR`, `FATAL`.
- All logs shipped to a centralized log store with < 1 minute availability SLA (99.9%).

#### Alerting

| Alert | Condition | Severity | Response |
|---|---|---|---|
| Daily PnL breach | PnL ≤ −12,500 USDT | P1 | Auto kill switch |
| Data staleness | Any feed > 2s stale | P1 | Block execution |
| Venue disconnected | 5 consecutive WS reconnect failures | P1 | Disable venue |
| Latency SLA breach | p95 e2e > 180 ms over 5 min window | P2 | Investigate |
| Reconciliation mismatch | Position diff > 0.5% | P1 | Block venue trading |
| Order reject rate spike | > 10% reject rate over 100 orders | P2 | Investigate |
| Strategy edge decay | Realized edge < 50% of expected over 1h | P2 | Review & tune |

**SLA targets from requirements**:
- Metrics ingestion delay (p95): ≤ 15 seconds.
- Critical alert delivery delay (p95): ≤ 30 seconds.
- Log availability: ≥ 99.9% within 1 minute.
- P1 acknowledgement: ≤ 5 minutes.
- P1 mitigation action started: ≤ 15 minutes.

---

### 5.10 Configuration Service

**Responsibility**: Centralized, version-controlled management of all runtime parameters.

**Configuration categories**:

| Category | Examples | Hot-reload |
|---|---|---|
| Strategy parameters | Edge thresholds, sizing limits, path definitions | Yes |
| Risk limits | Position caps, notional caps, daily loss cap, order limits | Yes (tighter only; loosening requires restart) |
| Venue settings | API endpoints, rate limits, fee overrides | Restart required |
| Cost model parameters | Default slippage curves, fee tier overrides | Yes |
| System tuning | Thread pool sizes, buffer capacities, timeouts | Restart required |

**Design**:
- Configuration stored in versioned YAML files, loaded via `spf13/viper` with struct tag binding and validated at load time using `go-playground/validator`.
- Viper's `fsnotify`-based file watcher detects changes and applies hot-reloadable parameters without restart. Updated config is atomically swapped via `atomic.Pointer[Config]` to avoid locking the hot path during reads.
- All configuration changes are logged with before/after values (via `slog`) for audit.
- Sensitive values (API keys, secrets) stored in environment variables and injected via Viper's `AutomaticEnv()` binding, never in YAML files. For production, integration with HashiCorp Vault is supported.

---

### 5.11 Persistence Layer

**Responsibility**: Durable storage for trade history, risk state checkpoints, audit logs, and analytics data.

**Storage tiers**:

| Tier | Technology | Data | Retention |
|---|---|---|---|
| Hot (in-memory) | Go structs + `sync.Map` / guarded maps | Active orders, positions, order books | Session lifetime |
| Warm (local) | SQLite via `modernc.org/sqlite` (pure Go) | Risk checkpoints, recent trades, order log | 30 days |
| Cold (remote) | PostgreSQL 16+ via `jackc/pgx` | Full trade history, PnL records, config audit log | Indefinite |
| Time-series | Prometheus (scraped via `/metrics` endpoint) | Metrics | 90 days (full res), 2 years (downsampled) |

**Write path** (hot path isolation):
- The critical trading path never blocks on persistence writes.
- Writes to warm/cold storage happen asynchronously via a dedicated `persistWriter` goroutine consuming from a buffered channel (`chan WriteRequest`).
- If the write channel fills (backpressure), non-critical writes are dropped with a metric increment; risk checkpoints use a separate, never-dropped channel.
- PostgreSQL writes use `pgx` batch mode to amortize round-trip cost when multiple writes are queued.

---

## 6. Data Flow

### 6.1 Triangular Arbitrage Flow

```
Step  Component               Action
────  ──────────────────────  ─────────────────────────────────────────────────
 1    Venue Gateway           Receives order book delta via WebSocket
 2    Market Data Service     Updates in-memory order book; publishes
                              OrderBookUpdate event
 3    Strategy Engine         TriArb module receives update; recomputes all
      (TriArb Module)         affected triangular paths
 4    Strategy Engine         Detects discrepancy ≥ 18 bps after costs;
                              emits TriArbSignal
 5    Cost Model Service      Provides real-time fee + slippage estimate
                              (called by Strategy Engine inline)
 6    Risk Manager            Validates signal against all risk checks:
                              - Position limits
                              - Notional limits
                              - Open order counts
                              - Data freshness
                              - Daily PnL headroom
 7    Risk Manager            Approved → forwards to Execution Engine
                              Rejected → logs reason, increments metric
 8    Execution Engine        Constructs 3-leg order sequence; submits to
                              Order Manager in rapid succession
 9    Order Manager           Assigns internal IDs; sends OrderRequests to
                              Venue Gateway
10    Venue Gateway           Signs and submits orders via REST API;
                              returns OrderAck/Reject
11    Order Manager           Processes fill/reject events; publishes
                              OrderStateChange events
12    Execution Engine        Monitors fill progress; adjusts for partial
                              fills or timeouts
13    Position Manager        Updates positions and PnL on each fill event
14    Risk Manager            Updates risk state on each position change
15    Monitoring              Records latency, edge realized vs expected,
                              fill slippage
```

**Latency budget allocation (p95 target: 180 ms total)**:

| Segment | Budget |
|---|---|
| Venue WS → Gateway processing | 5 ms |
| Gateway → Market Data Service update | 5 ms |
| Market Data → Strategy Engine computation | 20 ms |
| Strategy Engine → Risk Manager check | 10 ms |
| Risk Manager → Execution Engine | 5 ms |
| Execution Engine → Order submission (first leg) | 15 ms |
| Network round trip to venue (order ack) | 100 ms |
| Internal overhead & buffer | 20 ms |
| **Total** | **180 ms** |

### 6.2 Cross-Market Basis Arbitrage Flow

```
Step  Component               Action
────  ──────────────────────  ─────────────────────────────────────────────────
 1    Venue Gateway           Receives spot order book update + funding rate
                              update via WebSocket
 2    Market Data Service     Updates spot book and funding rate state;
                              publishes events
 3    Strategy Engine         BasisArb module computes annualized basis and
      (BasisArb Module)       funding regime
 4    Strategy Engine         Detects opportunity ≥ 22 bps net edge;
                              emits BasisArbSignal
 5    Cost Model Service      Provides fee + slippage + funding uncertainty
                              estimate
 6    Risk Manager            Validates against all limits (same as above)
 7    Execution Engine        Constructs 2-leg order (spot + perp hedge);
                              may use passive entry for better fill
 8    Order Manager           Manages order lifecycle for both legs
 9    Position Manager        Tracks hedged position state
10    Strategy Engine         Monitors basis and funding for exit signal
      (BasisArb Module)
11    Execution Engine        On exit signal: unwinds both legs
12    Position Manager        Updates to flat; computes realized PnL on
                              the cycle
```

---

## 7. Venue Integration

### 7.1 Connection Architecture

Each venue adapter maintains:
- **1 WebSocket connection** for public market data (shared across symbols).
- **1 WebSocket connection** for private/user data streams (order fills, balance updates) if supported by the venue.
- **REST API client** with connection pooling for order placement and account queries.

### 7.2 Rate Limit Management

Each venue adapter implements a **token bucket rate limiter**:

```go
type RateLimiter struct {
    buckets map[EndpointCategory]*TokenBucket
}

func (r *RateLimiter) Acquire(ctx context.Context, category EndpointCategory, weight int) error
func (r *RateLimiter) TryAcquire(category EndpointCategory, weight int) bool
```

- Categories: `public_data`, `private_data`, `order_place`, `order_cancel`, `account`.
- Weights reflect venue-specific rate limit accounting (e.g., some venues count order placement as heavier than data queries).
- When a bucket is exhausted, requests are queued with priority (order cancellations > order placements > data queries).

### 7.3 Symbol Mapping

Each venue adapter maintains a **symbol mapping table** that translates between internal canonical symbols and venue-specific symbols:

| Internal Symbol | Nobitex Symbol | KCEX Symbol |
|---|---|---|
| `BTC/USDT` (spot) | `BTCUSDT` | `BTC_USDT` |
| `ETH/USDT` (spot) | `ETHUSDT` | `ETH_USDT` |
| `SOL/USDT` (spot) | `SOLUSDT` | `SOL_USDT` |
| `BTCUSDT` (perp) | `BTCUSDT_PERP` | `BTCUSDT` |
| `ETHUSDT` (perp) | `ETHUSDT_PERP` | `ETHUSDT` |
| `SOLUSDT` (perp) | `SOLUSDT_PERP` | `SOLUSDT` |

*(Exact venue symbols are illustrative and must be confirmed during integration.)*

---

## 8. Risk Management Architecture

### 8.1 Defense in Depth

Risk controls are layered:

```
Layer 1: Strategy Engine     — Pre-filters signals below profitability thresholds
Layer 2: Risk Manager        — Enforces hard limits (position, notional, PnL, orders)
Layer 3: Execution Engine    — Timeout and partial fill protection
Layer 4: Venue Gateway       — Rate limiting, connection health checks
Layer 5: Venue (External)    — Exchange-side position limits, margin requirements
```

### 8.2 Risk State Machine

```
                     ┌─────────┐
                     │ NORMAL  │
                     └────┬────┘
                          │
              ┌───────────┼───────────┐
              ▼           ▼           ▼
        ┌──────────┐ ┌────────┐ ┌──────────┐
        │ WARNING  │ │DEGRADED│ │DATA_STALE│
        │ (80%     │ │(venue  │ │(feed >   │
        │  limit)  │ │ issue) │ │ 500ms)   │
        └────┬─────┘ └───┬────┘ └────┬─────┘
             │           │           │
             ▼           ▼           ▼
        ┌─────────────────────────────────┐
        │            HALTED               │
        │  (PnL breach / kill switch /    │
        │   unrecoverable error)          │
        │  → Cancel all, flatten, stop    │
        │  → Requires manual resume       │
        └─────────────────────────────────┘
```

### 8.3 Daily PnL Tracking

- PnL accumulates from UTC 00:00:00 and resets daily.
- Calculated as: `realized_pnl + mark_to_market_unrealized_pnl`.
- Checked on every fill event and every 1-second periodic tick.
- At −10,000 USDT (80% of cap): `WARNING` state, alerts fired, new signal sizing reduced by 50%.
- At −12,500 USDT: `HALTED` state, kill switch triggered.

---

## 9. Latency Architecture

Meeting the 180 ms e2e latency target (p95) requires deliberate architectural choices:

### 9.1 Hot-Path Optimizations

| Technique | Component | Impact |
|---|---|---|
| **Lock-free ring buffer** | Event bus | Uses `sync/atomic` operations; eliminates mutex contention on the critical path |
| **`sync.Pool` object pooling** | Market Data, Strategy Engine | Reuses allocated structs for order book updates and signals, reducing GC pressure |
| **Pre-computed lookup tables** | Strategy Engine | Eliminates repeated triangular path enumeration |
| **`GOGC` tuning** | Runtime | Set `GOGC=400` or higher to trade memory for fewer GC cycles on the hot path |
| **`GOMEMLIMIT`** | Runtime | Hard memory ceiling prevents OOM while allowing aggressive `GOGC` tuning |
| **Connection keep-alive** | Venue Gateway | Persistent `*http.Client` with keep-alive and connection pooling avoids TLS/TCP handshake per order |
| **Batch-free processing** | All hot-path components | Each event processed immediately via dedicated goroutines, no micro-batching |
| **Pre-allocated channel buffers** | Event bus, gateway | Buffered channels sized to absorb burst traffic without blocking senders |

### 9.2 Concurrency Model (Goroutines)

Go's lightweight goroutines and channels replace traditional thread pools. The scheduler multiplexes goroutines onto OS threads automatically, but `GOMAXPROCS` and runtime pinning are used to control hot-path locality.

```
Goroutine                 Responsibility                     Notes
────────────────────      ──────────────────────────────     ──────────────
mdReceiver (×N)           WebSocket read + parse             One per venue WS connection;
                                                             reads into buffered channel
eventDispatcher           Fan-out events to consumers        Single goroutine; select on
                                                             input channels, write to
                                                             subscriber channels
strategyWorker (×M)       Signal computation                 One per venue; consumes from
                                                             market data channel
riskChecker               Synchronous risk validation        Single goroutine; serializes
                                                             all risk checks to avoid locks
orderSubmitter (×K)       HTTP POST to venue REST API        Goroutine pool via semaphore;
                                                             bounded concurrency
persistWriter             Async DB writes                    Consumes from write channel;
                                                             batches inserts
metricsEmitter            Periodic metric flush              Ticker-driven goroutine
reconciler                Periodic position reconciliation   Ticker-driven goroutine
```

- `GOMAXPROCS` is set to match the number of available CPU cores.
- For extreme latency sensitivity, the `runtime.LockOSThread()` call can pin critical goroutines (e.g., `mdReceiver`, `strategyWorker`) to dedicated OS threads, preventing scheduler preemption.
- Channel backpressure is monitored: if a channel reaches 80% capacity, a warning metric is emitted; at 100%, the sender drops with a counter increment (non-critical paths only).

### 9.3 Latency Measurement

- Latency is measured at three points per the requirements:
  1. **Market data to decision**: timestamp at MD receive → timestamp at signal emit.
  2. **Decision to order ack**: timestamp at signal emit → timestamp at venue order acknowledgement.
  3. **End-to-end tick-to-ack**: timestamp at MD receive → timestamp at venue order acknowledgement.
- Measurements use Go's **monotonic clock** (`time.Now()` includes monotonic reading; elapsed via `time.Since()`) for intra-process segments.
- Histograms with percentile tracking (p50, p95, p99) are reported every 10 seconds.

---

## 10. Deployment Architecture

### 10.1 Infrastructure

```
┌──────────────────────────────────────────────────────┐
│                Production Environment                 │
│                                                      │
│  ┌────────────────┐     ┌────────────────┐           │
│  │  Trading Node  │     │  Monitoring    │           │
│  │  (Primary)     │     │  Node          │           │
│  │                │     │                │           │
│  │  • All trading │     │  • Prometheus  │           │
│  │    components  │     │  • Grafana     │           │
│  │  • Local DB    │     │  • Alertmanager│           │
│  │                │     │  • Log agg.    │           │
│  └───────┬────────┘     └───────┬────────┘           │
│          │                      │                    │
│          └──────────┬───────────┘                    │
│                     │                                │
│              ┌──────▼───────┐                        │
│              │  PostgreSQL  │                        │
│              │  (Cold store)│                        │
│              └──────────────┘                        │
└──────────────────────────────────────────────────────┘
```

### 10.2 Deployment Strategy

- **Single-node deployment** in V1 for simplicity and latency minimization (no inter-node network hops on the hot path).
- The trading node runs all core components (Market Data, Strategy, Risk, Execution, Gateways) in a single process with shared memory.
- Monitoring runs on a separate node to avoid resource contention with the hot path.
- PostgreSQL may be co-located or on a separate node depending on I/O profile.

### 10.3 High Availability (V1)

- V1 does **not** implement active-active redundancy.
- A **warm standby** node can be provisioned with the same configuration, ready to take over manually.
- The kill switch ensures that on primary failure, no stale orders are left unmanaged (venue-side TTL and manual monitoring provide the safety net).
- Monthly uptime targets:
  - Strategy engine: ≥ 99.5% (allows ~3.6 hours downtime/month).
  - Order routing service: ≥ 99.9% (allows ~43 minutes downtime/month).

### 10.4 Containerization

- Multi-stage Docker build: `golang:1.22-alpine` for building, `scratch` (or `gcr.io/distroless/static`) for the final image containing only the static binary + CA certificates.
- Typical production image size: **~15 MB**.
- `docker-compose` for local development and staging (includes PostgreSQL, Prometheus, Grafana sidecars).
- Single-container deployment for the trading process in production (to avoid container networking overhead on the hot path), with sidecar containers for monitoring agents.
- SQL migrations and default config are embedded in the binary via `//go:embed`, eliminating volume mount dependencies.

---

## 11. Security Architecture

### 11.1 API Key Management

- Venue API keys and secrets are stored in a **secrets manager** (e.g., HashiCorp Vault, AWS Secrets Manager, or encrypted environment variables).
- Keys are loaded into memory at startup and never written to disk, logs, or config files.
- API keys use **IP whitelisting** on the venue side, restricted to the trading node's static IP.
- Separate API keys for production and staging/testing environments.
- Keys have the **minimum required permissions** (trade + read; no withdrawal in V1).

### 11.2 Network Security

- All venue communication over **TLS 1.2+** with certificate validation.
- Trading node placed in a **private network** with no inbound public access.
- Monitoring and management access via **VPN or SSH tunnel** only.
- Outbound traffic restricted to venue API endpoints and monitoring infrastructure via firewall rules.

### 11.3 Application Security

- No user-facing web interface in V1; all interaction via CLI and configuration files.
- Configuration changes require authenticated access to the deployment host.
- Audit log captures all configuration changes, kill switch events, and manual interventions.

### 11.4 Data Security

- Sensitive data (API keys, account balances) is **never logged** even at DEBUG level.
- Trade data at rest encrypted if stored on shared infrastructure.
- Log shipping to centralized store uses TLS-encrypted transport.

---

## 12. Technology Stack

The system is implemented in **Go**, chosen for its compiled performance, lightweight concurrency model (goroutines + channels), low and predictable GC latency, single-binary deployment, and strong standard library support for networking.

| Layer | Technology | Rationale |
|---|---|---|
| **Core language** | Go 1.22+ | Compiled binary with sub-millisecond GC pauses, goroutine-based concurrency, strong typing, and a rich standard library for HTTP/WebSocket/crypto. Single binary deployment simplifies operations. |
| **Concurrency** | Goroutines + channels | Native CSP-style concurrency maps naturally to the event-driven pipeline. Channels provide type-safe, bounded message passing between components. |
| **WebSocket client** | [`gorilla/websocket`](https://github.com/gorilla/websocket) or [`nhooyr.io/websocket`](https://github.com/nhooyr/websocket) | Mature, production-grade WebSocket implementations. `nhooyr.io/websocket` offers `context.Context` integration and is preferred for new code. |
| **HTTP client** | `net/http` (stdlib) | Go's standard HTTP client supports connection pooling, keep-alive, timeouts, and TLS out of the box. No external dependency needed. |
| **Decimal arithmetic** | [`shopspring/decimal`](https://github.com/shopspring/decimal) | Go has no native decimal type. This is the most widely adopted arbitrary-precision decimal library in the Go ecosystem. Wraps `math/big` internally for exact arithmetic. See [Section 12.3](#123-numeric-precision-strategy) for the performance trade-off and `int64` fixed-point optimization on the hot path. |
| **UUID generation** | [`google/uuid`](https://github.com/google/uuid) | UUIDv7 (time-ordered) for internal order IDs, enabling chronological sorting without extra indexing. |
| **Configuration** | [`spf13/viper`](https://github.com/spf13/viper) + YAML files | File-based config with environment variable override, hot-reload via `fsnotify` watcher, and struct tag-based binding. |
| **Configuration validation** | [`go-playground/validator`](https://github.com/go-playground/validator) | Struct tag-based validation for configuration structs at load time (e.g., `validate:"required,gt=0"`). |
| **Local persistence** | [`mattn/go-sqlite3`](https://github.com/mattn/go-sqlite3) (CGo) or [`modernc.org/sqlite`](https://gitlab.com/ACP86/sqlite) (pure Go) | Risk checkpoints and recent trade log. Pure Go option avoids CGo cross-compilation complexity. |
| **Cold storage** | PostgreSQL 16+ via [`jackc/pgx`](https://github.com/jackc/pgx) | High-performance PostgreSQL driver with connection pooling (`pgxpool`), binary protocol, and batch query support. |
| **Database migrations** | [`golang-migrate/migrate`](https://github.com/golang-migrate/migrate) | Version-controlled SQL migration files applied at startup. |
| **Metrics** | [`prometheus/client_golang`](https://github.com/prometheus/client_golang) | Native Prometheus client with histogram, gauge, and counter support. Exposes `/metrics` HTTP endpoint. |
| **Tracing** | [OpenTelemetry Go SDK](https://github.com/open-telemetry/opentelemetry-go) | Distributed tracing with trace context propagation across goroutines. Exports to Jaeger or OTLP-compatible backends. |
| **Logging** | [`log/slog`](https://pkg.go.dev/log/slog) (stdlib, Go 1.21+) | Structured JSON logging with context binding. Zero-dependency, high performance. Can be extended with custom handlers for log shipping. |
| **Monitoring dashboard** | Grafana | Visualization of Prometheus metrics, traces (via Tempo/Jaeger), and logs (via Loki). |
| **Alerting** | Prometheus Alertmanager + Telegram/PagerDuty integration | Multi-channel critical alert delivery within 30s SLA. |
| **Containerization** | Docker (multi-stage build) | Minimal scratch-based image containing only the static Go binary + CA certs. Typical image size: ~15 MB. |
| **Build & CI** | `go build`, `go test`, `golangci-lint`, `goreleaser` | Standard Go toolchain. `golangci-lint` enforces code quality. `goreleaser` produces release binaries and Docker images. |
| **Testing** | `testing` (stdlib) + [`stretchr/testify`](https://github.com/stretchr/testify) + [`uber-go/mock`](https://github.com/uber-go/mock) | Table-driven tests, mock generation for interfaces, benchmarks via `testing.B`, and race detection via `go test -race`. |

### 12.1 Key Go-Specific Design Choices

| Decision | Detail |
|---|---|
| **Interface-based composition** | All major components (VenueGateway, Strategy, RiskChecker, CostModel) are defined as Go interfaces. This enables dependency injection, testability via mocks, and the dry-run simulated gateway swap. |
| **`context.Context` propagation** | Every external call (venue API, database) and long-running goroutine accepts a `context.Context` for cancellation and timeout propagation. The kill switch cancels the root context, triggering graceful shutdown across all goroutines. |
| **Error handling** | Errors are returned explicitly (no panics on the hot path). Sentinel errors and custom error types are used for classifying venue errors (transient vs. permanent) to drive retry logic. |
| **`sync.Pool` for hot-path allocations** | Order book update structs, signal objects, and serialization buffers are pooled to minimize heap allocations on the critical path. |
| **`GOGC` and `GOMEMLIMIT` tuning** | Production runtime is configured with `GOGC=400` and `GOMEMLIMIT=2GiB` (adjusted per deployment) to reduce GC frequency while bounding memory usage. |
| **Single binary deployment** | `CGO_ENABLED=0` static build (or `CGO_ENABLED=1` only if using `go-sqlite3`). The entire application ships as one binary with embedded config schema and migration files via `embed`. |
| **`go:embed` for static assets** | SQL migration files and default configuration schemas are embedded into the binary at compile time using `//go:embed` directives, eliminating filesystem dependencies at runtime. |

### 12.2 Dependency Summary

```
go 1.22

require (
    github.com/gorilla/websocket   v1.5.x    // or nhooyr.io/websocket v1.8.x
    github.com/shopspring/decimal   v1.4.x
    github.com/google/uuid          v1.6.x
    github.com/spf13/viper          v1.18.x
    github.com/jackc/pgx/v5         v5.6.x
    github.com/prometheus/client_golang v1.19.x
    go.opentelemetry.io/otel        v1.28.x
    github.com/stretchr/testify     v1.9.x
    github.com/golang-migrate/migrate/v4 v4.17.x
    modernc.org/sqlite              v1.30.x   // pure-Go SQLite
    github.com/go-playground/validator/v10 v10.22.x
)
```

### 12.3 Numeric Precision Strategy

Go lacks a native decimal type. All financial arithmetic uses `shopspring/decimal.Decimal`, which provides exact decimal representation via `math/big` internally. However, `decimal.Decimal` has a performance cost (heap allocations, arbitrary-precision math) that must be managed on the hot path.

**Two-tier approach:**

| Tier | Type | Where used | Rationale |
|---|---|---|---|
| **Canonical (storage & APIs)** | `decimal.Decimal` | Order prices/sizes, position tracking, PnL, risk state, cost estimates, persistence, venue API serialization | Correctness is non-negotiable. These values flow through risk checks, accumulate across trades, and are persisted. Rounding errors here produce wrong trade decisions or incorrect PnL reporting. |
| **Hot-path inner loop** | `int64` fixed-point | Signal detection comparisons inside the Strategy Engine (e.g., "is implied cross rate > direct rate + 18 bps?") | The inner loop of triangular arb detection runs on every order book tick. Converting to `int64` micro-units avoids heap allocation per comparison. |

**Fixed-point convention:**

```go
const PricePrecision = 1_000_000_000 // 9 decimal places (nano-units)

type FixedPrice int64

func ToFixed(d decimal.Decimal) FixedPrice {
    return FixedPrice(d.Mul(decimal.NewFromInt(PricePrecision)).IntPart())
}

func (f FixedPrice) ToDecimal() decimal.Decimal {
    return decimal.New(int64(f), -9)
}
```

- Order book price levels are stored as both `decimal.Decimal` (for order submission) and `FixedPrice` (for signal math) when the book is updated. The conversion happens once per update, not once per comparison.
- The threshold comparisons in the strategy engine use `FixedPrice` arithmetic (plain `int64` add/subtract/compare — no allocations).
- Once a signal is detected and passes the threshold, all downstream processing (cost model, risk check, order construction) uses `decimal.Decimal` exclusively.

**Why not `float64` anywhere?**

- `float64` cannot exactly represent `0.1`, `0.01`, or most decimal fractions. In a three-leg triangular arb, this means `leg1 * leg2 * leg3` produces a different result than the mathematically correct value, and the error is unpredictable.
- `int64` fixed-point has none of these problems: it is exact within its precision range, uses no heap, and is faster than `float64` for integer comparisons.
- `decimal.Decimal` is exact at any precision, at the cost of heap allocation per operation.
- The system never uses `float64` for any price, size, fee, or PnL value.

---

## 13. Project Layout

The project follows the [Standard Go Project Layout](https://github.com/golang-standards/project-layout) conventions, adapted for a single-binary trading system.

```
trading/
├── cmd/
│   └── trader/
│       └── main.go                 # Application entry point, wires dependencies
│
├── internal/                       # Private application code (not importable by external projects)
│   ├── config/
│   │   ├── config.go               # Config structs with viper binding + validator tags
│   │   └── loader.go               # Load, validate, watch for hot-reload
│   │
│   ├── domain/                     # Core domain types shared across packages
│   │   ├── order.go                # Order, OrderStatus, LegSpec
│   │   ├── position.go             # Position, Balance
│   │   ├── signal.go               # TradeSignal, StrategyType
│   │   ├── book.go                 # OrderBookSnapshot, PriceLevel
│   │   ├── risk.go                 # RiskState, RiskMode
│   │   └── fixedpoint.go           # FixedPrice int64 type + decimal conversion
│   │
│   ├── marketdata/
│   │   ├── service.go              # Market Data Service: book maintenance, staleness
│   │   └── ringbuffer.go           # Lock-free ring buffer for trade ticks
│   │
│   ├── strategy/
│   │   ├── engine.go               # Strategy Engine: dispatches to modules
│   │   ├── triarb.go               # Triangular arbitrage detection
│   │   └── basisarb.go             # Cross-market basis arbitrage detection
│   │
│   ├── risk/
│   │   ├── manager.go              # Risk Manager: limit checks, state machine
│   │   ├── killswitch.go           # Kill switch logic and persistence
│   │   └── pnl.go                  # Daily PnL tracker
│   │
│   ├── execution/
│   │   ├── engine.go               # Execution Engine: leg sequencing, timeout, retry
│   │   └── quality.go              # Fill quality / slippage tracking
│   │
│   ├── order/
│   │   ├── manager.go              # Order Manager: lifecycle, dedup, state events
│   │   └── idgen.go                # UUIDv7 order ID generator
│   │
│   ├── costmodel/
│   │   ├── service.go              # Cost Model Service: fee + slippage estimation
│   │   └── slippage.go             # Piecewise linear slippage curves
│   │
│   ├── portfolio/
│   │   ├── manager.go              # Position & Portfolio Manager
│   │   └── reconciler.go           # Periodic venue reconciliation
│   │
│   ├── gateway/                    # Venue Gateway Layer
│   │   ├── gateway.go              # VenueGateway interface definition
│   │   ├── nobitex/
│   │   │   ├── adapter.go          # Nobitex gateway implementation
│   │   │   ├── ws.go               # WebSocket connection management
│   │   │   └── rest.go             # REST API client + signing
│   │   ├── kcex/
│   │   │   ├── adapter.go          # KCEX gateway implementation
│   │   │   ├── ws.go               # WebSocket connection management
│   │   │   └── rest.go             # REST API client + signing
│   │   ├── simulated/
│   │   │   ├── adapter.go          # Simulated (dry-run) gateway
│   │   │   └── fillsim.go          # Fill simulation engine
│   │   └── ratelimit.go            # Token bucket rate limiter
│   │
│   ├── eventbus/
│   │   └── bus.go                  # In-process pub/sub with typed channels
│   │
│   ├── persistence/
│   │   ├── sqlite.go               # SQLite checkpoint store
│   │   ├── postgres.go             # PostgreSQL cold store client
│   │   └── writer.go               # Async write goroutine with backpressure
│   │
│   └── monitor/
│       ├── metrics.go              # Prometheus metric definitions + registration
│       ├── tracing.go              # OpenTelemetry tracer setup
│       └── alerts.go               # Alert rule evaluation + notification dispatch
│
├── migrations/                     # SQL migration files (embedded via //go:embed)
│   ├── 001_create_trades.up.sql
│   ├── 001_create_trades.down.sql
│   ├── 002_create_strategy_cycles.up.sql
│   └── ...
│
├── configs/                        # Example / default configuration files
│   ├── config.example.yaml
│   └── config.schema.json          # Optional JSON Schema for editor validation
│
├── scripts/                        # Development and deployment helpers
│   ├── docker-compose.yml          # Local dev stack (Postgres, Prometheus, Grafana)
│   └── Makefile                    # build, test, lint, run targets
│
├── Dockerfile                      # Multi-stage build → scratch image
├── go.mod
├── go.sum
└── README.md
```

**Package dependency rules**:

- `domain` depends on nothing internal (only stdlib + `shopspring/decimal`).
- `gateway`, `strategy`, `risk`, `execution`, `costmodel`, `portfolio`, `order` depend on `domain` and optionally on each other's interfaces (never concrete types).
- `cmd/trader/main.go` is the sole composition root: it creates all concrete implementations and wires them together.
- No package imports `cmd/` or `internal/` from another package at the same level — all dependencies flow inward toward `domain`.

---

## 14. Data Model

### 14.1 Core Entities

```go
// --- Enums (typed constants) ---

type Side string
const (
    SideBuy  Side = "BUY"
    SideSell Side = "SELL"
)

type InstrumentType string
const (
    InstrumentSpot InstrumentType = "SPOT"
    InstrumentPerp InstrumentType = "PERP"
)

type OrderType string
const (
    OrderTypeLimit  OrderType = "LIMIT"
    OrderTypeMarket OrderType = "MARKET"
)

type OrderStatus string
const (
    OrderStatusPendingNew   OrderStatus = "PENDING_NEW"
    OrderStatusSubmitted    OrderStatus = "SUBMITTED"
    OrderStatusAcknowledged OrderStatus = "ACKNOWLEDGED"
    OrderStatusPartialFill  OrderStatus = "PARTIAL_FILL"
    OrderStatusFilled       OrderStatus = "FILLED"
    OrderStatusCancelled    OrderStatus = "CANCELLED"
    OrderStatusRejected     OrderStatus = "REJECTED"
    OrderStatusSubmitFailed OrderStatus = "SUBMIT_FAILED"
)

type StrategyType string
const (
    StrategyTriArb   StrategyType = "TRI_ARB"
    StrategyBasisArb StrategyType = "BASIS_ARB"
)

type RiskMode string
const (
    RiskModeNormal    RiskMode = "NORMAL"
    RiskModeWarning   RiskMode = "WARNING"
    RiskModeDegraded  RiskMode = "DEGRADED"
    RiskModeDataStale RiskMode = "DATA_STALE"
    RiskModeHalted    RiskMode = "HALTED"
)

// --- Core structs ---

type PriceLevel struct {
    Price decimal.Decimal
    Size  decimal.Decimal
}

type OrderBookSnapshot struct {
    Venue          string
    Symbol         string
    Bids           []PriceLevel  // sorted descending by price
    Asks           []PriceLevel  // sorted ascending by price
    Sequence       uint64
    VenueTimestamp time.Time
    LocalTimestamp time.Time
}

type TradeSignal struct {
    SignalID            uuid.UUID
    Strategy            StrategyType
    Venue               string
    Legs                []LegSpec
    ExpectedEdgeBps     decimal.Decimal
    CostEstimate        CostEstimate
    Confidence          decimal.Decimal
    CreatedAt           time.Time
    MarketDataTimestamp time.Time
}

type LegSpec struct {
    Symbol         string
    Side           Side
    InstrumentType InstrumentType
    Price          decimal.Decimal
    Size           decimal.Decimal
    OrderType      OrderType
}

type Order struct {
    InternalID   uuid.UUID
    VenueID      string
    SignalID     uuid.UUID
    Venue        string
    Symbol       string
    Side         Side
    OrderType    OrderType
    Price        decimal.Decimal
    Size         decimal.Decimal
    FilledSize   decimal.Decimal
    AvgFillPrice decimal.Decimal
    Status       OrderStatus
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type Position struct {
    Venue          string
    Asset          string
    InstrumentType InstrumentType
    Size           decimal.Decimal // positive = long, negative = short
    EntryPrice     decimal.Decimal
    UnrealizedPnL  decimal.Decimal
    MarginUsed     decimal.Decimal // perp only
    UpdatedAt      time.Time
}

type VenueAssetKey struct {
    Venue string
    Asset string
}

type RiskState struct {
    Mode              RiskMode
    DailyRealizedPnL  decimal.Decimal
    DailyUnrealizedPnL decimal.Decimal
    Positions         map[VenueAssetKey]*Position
    OpenOrderCounts   OrderCountState
    VenueNotionals    map[string]decimal.Decimal
    LastCheckpoint    time.Time
    KillSwitchActive  bool
    KillSwitchReason  string // empty when inactive
}

type OrderCountState struct {
    Global   int
    PerVenue map[string]int
    PerSymbol map[string]int
}
```

### 14.2 Database Schema (Cold Store)

```sql
-- Trade history
CREATE TABLE trades (
    id              UUID PRIMARY KEY,
    signal_id       UUID NOT NULL,
    strategy        VARCHAR(32) NOT NULL,
    venue           VARCHAR(32) NOT NULL,
    symbol          VARCHAR(32) NOT NULL,
    side            VARCHAR(4) NOT NULL,
    instrument_type VARCHAR(8) NOT NULL,
    price           NUMERIC(20, 8) NOT NULL,
    size            NUMERIC(20, 8) NOT NULL,
    fee             NUMERIC(20, 8) NOT NULL,
    fee_currency    VARCHAR(8) NOT NULL,
    venue_order_id  VARCHAR(64),
    venue_trade_id  VARCHAR(64),
    executed_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Strategy cycle outcomes
CREATE TABLE strategy_cycles (
    id                  UUID PRIMARY KEY,
    strategy            VARCHAR(32) NOT NULL,
    venue               VARCHAR(32) NOT NULL,
    signal_id           UUID NOT NULL,
    expected_edge_bps   NUMERIC(10, 4),
    realized_edge_bps   NUMERIC(10, 4),
    total_fees          NUMERIC(20, 8),
    total_slippage_bps  NUMERIC(10, 4),
    pnl_usdt            NUMERIC(20, 8),
    status              VARCHAR(16) NOT NULL,  -- completed, partial, aborted
    started_at          TIMESTAMPTZ NOT NULL,
    completed_at        TIMESTAMPTZ,
    metadata            JSONB
);

-- Daily PnL snapshots
CREATE TABLE daily_pnl (
    date            DATE PRIMARY KEY,
    realized_pnl    NUMERIC(20, 8) NOT NULL,
    unrealized_pnl  NUMERIC(20, 8) NOT NULL,
    total_pnl       NUMERIC(20, 8) NOT NULL,
    num_cycles      INTEGER NOT NULL,
    num_trades      INTEGER NOT NULL,
    fees_paid       NUMERIC(20, 8) NOT NULL,
    funding_net     NUMERIC(20, 8) NOT NULL
);

-- Risk events
CREATE TABLE risk_events (
    id          UUID PRIMARY KEY,
    event_type  VARCHAR(32) NOT NULL,
    severity    VARCHAR(4) NOT NULL,
    details     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Configuration audit log
CREATE TABLE config_audit (
    id          UUID PRIMARY KEY,
    key         VARCHAR(128) NOT NULL,
    old_value   TEXT,
    new_value   TEXT NOT NULL,
    changed_by  VARCHAR(64) NOT NULL,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## 15. Dry Run / Paper Trading Mode

The system supports a **dry run (paper trading) mode** that simulates the full trading pipeline — from signal detection through execution — without placing real orders on any venue. This mode is essential for strategy validation, system integration testing, cost model calibration, and operator training before committing real capital.

### 15.1 Overview

When dry run mode is enabled, the system behaves identically to live trading with one critical difference: the Execution Engine routes orders to a **Simulated Venue Gateway** instead of the real venue adapters. All other components — Market Data Service, Strategy Engine, Risk Manager, Cost Model, Position Manager, Monitoring — operate normally against **live market data**.

```
                          ┌──────────────────────────┐
                          │   Mode: DRY_RUN          │
                          │                          │
  Live Market Data ──────►│  Market Data Service     │
                          │        │                 │
                          │  Strategy Engine         │
                          │        │                 │
                          │  Risk Manager            │
                          │        │                 │
                          │  Execution Engine        │
                          │        │                 │
                          │  ┌─────▼───────────────┐ │
                          │  │ Simulated Venue GW   │ │  ← No real orders
                          │  │ (Fill Simulator)     │ │
                          │  └─────────────────────┘ │
                          │        │                 │
                          │  Position Manager        │  ← Tracks simulated positions
                          │  Monitoring / Logs       │  ← Full observability
                          └──────────────────────────┘
```

### 15.2 Operating Modes

The system supports three operating modes, controlled by a single configuration parameter:

| Mode | Config value | Market data | Signal detection | Risk checks | Order execution | PnL tracking |
|---|---|---|---|---|---|---|
| **Live** | `live` | Real (venue WS) | Active | Enforced | Real orders to venue | Real |
| **Dry run** | `dry_run` | Real (venue WS) | Active | Enforced | Simulated locally | Simulated |
| **Backtest** | `backtest` | Historical replay | Active | Enforced | Simulated locally | Simulated |

The `dry_run` mode is the focus of this section. Backtest mode shares the same simulation engine but replays historical data instead of consuming live feeds; it is a post-V1 enhancement but the architecture accommodates it.

### 15.3 Simulated Venue Gateway

The Simulated Venue Gateway implements the same `VenueGateway` interface as the real adapters (see [Section 5.8](#58-venue-gateway-layer)), making it a drop-in replacement with no changes to upstream components.

**Fill simulation logic**:

| Behavior | Detail |
|---|---|
| **Market orders** | Filled immediately at the current best bid/ask from the live order book, applying the configured slippage model. |
| **Limit orders** | Filled when the live market price crosses the limit price. Partial fills are simulated based on order book depth at the crossed level. |
| **Latency simulation** | A configurable artificial delay (default: 50 ms) is injected between order submission and acknowledgement to mimic real venue round-trip latency. |
| **Fee application** | Simulated fills apply the same fee schedule as the real venue (maker/taker rates from the Cost Model Service). |
| **Reject simulation** | Optionally injects order rejects at a configurable rate (default: 0%) to test error handling paths. |
| **Funding rate** | For perp positions held in dry run, funding payments are calculated from the live funding rate stream and applied to simulated PnL. |

**Fill model configuration**:

```go
type SimulatedFill struct {
    FillPrice decimal.Decimal // best bid/ask ± slippage
    FillSize  decimal.Decimal // min(order_size, available_liquidity)
    Fee       decimal.Decimal // from cost model
    LatencyMs int             // simulated venue RTT
    Status    OrderStatus     // FILLED, PARTIAL_FILL, or REJECTED
}

type FillSimulator interface {
    SimulateFill(order OrderRequest, book *OrderBookSnapshot) (*SimulatedFill, error)
}
```

### 15.4 Simulated Position & PnL Tracking

In dry run mode, the Position & Portfolio Manager tracks simulated positions in a **separate namespace** from any real positions:

- Simulated balances start from a configurable **initial capital** (default: 100,000 USDT).
- Positions are updated on every simulated fill event, using the same logic as live mode.
- PnL is computed identically to live mode (mark-to-market against live prices).
- Daily PnL loss cap and all risk limits are enforced against the simulated portfolio, so the system behaves exactly as it would in production.

### 15.5 Observability in Dry Run

All metrics, traces, and logs emitted during dry run are tagged with `mode: dry_run` to distinguish them from live trading data:

- Metrics carry a `mode` label: `realized_edge_bps{mode="dry_run", strategy="tri_arb", ...}`.
- Logs include a `"mode": "dry_run"` field in every structured log entry.
- A dedicated **Grafana dashboard** (or dashboard tab) displays dry run performance side-by-side with live performance once live trading begins.

**Key dry run metrics**:

| Metric | Purpose |
|---|---|
| `dry_run_signals_total` | Total signals generated during paper trading |
| `dry_run_simulated_fills_total` | Total simulated fills |
| `dry_run_pnl_usdt` | Cumulative simulated PnL |
| `dry_run_edge_realized_bps` | Realized edge on simulated trades (validates strategy profitability) |
| `dry_run_slippage_model_error_bps` | Difference between modeled slippage and what live book depth would have produced |
| `dry_run_signal_to_stale_pct` | Percentage of signals that became stale before simulated fill (latency proxy) |

### 15.6 Dry Run Safeguards

| Safeguard | Detail |
|---|---|
| **Mode is explicit and persistent** | The operating mode is set in the configuration file and logged at startup. There is no implicit fallback to live mode. |
| **Startup confirmation** | On startup in `live` mode, the system logs a prominent `LIVE TRADING ACTIVE` warning and optionally requires a CLI confirmation flag (`--confirm-live`). |
| **No real API key requirement** | Dry run mode does not require valid trading API keys. Market data API keys are still needed for live feed access. |
| **Isolated persistence** | Dry run trades are written to a separate database table (`dry_run_trades`) or tagged with a `mode` column, preventing confusion with real trade history. |
| **No venue-side effects** | The Simulated Venue Gateway never opens network connections to venue trading endpoints; only market data connections are used. |

### 15.7 Transition from Dry Run to Live

The recommended promotion workflow:

```
1. Deploy in dry_run mode with full production configuration
2. Run for ≥ 72 hours (covering multiple funding intervals and market regimes)
3. Review dry run performance report:
   - Is realized edge within ±30% of expected edge?
   - Is simulated slippage within ±50% of model predictions?
   - Are risk limits never breached unexpectedly?
   - Are all monitoring alerts firing correctly?
   - Is latency within SLA targets?
4. If satisfactory:
   a. Reduce risk limits to 25% of production values
   b. Switch mode to live (set trading_mode: live + --confirm-live flag)
   c. Monitor first 4 hours intensively
   d. Gradually increase risk limits to production values over 48 hours
5. If unsatisfactory:
   - Tune strategy parameters, cost model, or slippage curves
   - Repeat from step 2
```

---

## 16. Failure Modes & Recovery

| Failure Mode | Detection | Automated Response | Manual Follow-up |
|---|---|---|---|
| **Venue WebSocket disconnect** | Heartbeat timeout | Reconnect with exponential backoff; block trading after 5 failures | Verify venue status; check IP whitelist |
| **Stale market data** | Freshness SLA (>500 ms) | Block new signal processing; existing orders remain | Investigate feed; switch to REST fallback |
| **Order submission failure** | Venue reject / timeout | Retry 2× for transient errors; cancel cycle for persistent errors | Check rate limits, API key validity |
| **Partial fill on tri-arb leg** | Fill monitoring timeout | Hedge residual exposure; cancel unfilled legs | Review fill quality; adjust sizing |
| **Position reconciliation mismatch** | Periodic reconciliation | Block trading for affected venue; raise P1 alert | Manual position verification and correction |
| **Daily PnL breach** | PnL check on every fill | Kill switch: cancel all orders, flatten exposure | Root cause analysis; manual resume |
| **Process crash / panic** | Process monitor (systemd / Docker restart policy) | Auto-restart; `recover()` in top-level goroutines prevents single-goroutine panics from crashing the process; on full restart, state is reconstructed from checkpoint + venue queries | Verify state consistency; review crash logs and goroutine stack dump |
| **Database write failure** | Write thread error count | Buffer writes in memory; retry; alert if buffer nears capacity | Fix DB connectivity; replay buffered writes |
| **Configuration error** | Schema validation | Reject invalid config; continue with last valid config | Fix configuration; verify schema |

### 16.1 Startup & Recovery Sequence

```
1. Load configuration (validate schema)
2. Initialize logging and metrics
3. Connect to persistent storage
4. Load last risk state checkpoint
5. Connect to venues (authenticate, query positions/balances)
6. Reconcile risk state checkpoint against venue positions
   → If mismatch > threshold: enter DEGRADED mode, alert, require manual resolution
   → If mismatch within threshold: adopt venue-authoritative state
7. Subscribe to market data feeds
8. Wait for market data freshness SLA to be satisfied
9. Check kill switch state
   → If active: remain in HALTED mode, await manual resume
   → If inactive: enter NORMAL mode, begin signal processing
```

---

## 17. Future Considerations (Post-V1)

The architecture is designed with the following future expansions in mind, though none are in V1 scope:

| Feature | Architectural Impact |
|---|---|
| **Spatial (cross-venue) arbitrage** | Requires transfer/withdrawal automation, cross-venue position netting, and treasury routing. The venue gateway abstraction and position manager are pre-designed to support multi-venue coordination. |
| **Additional exchanges** | New venue gateway adapters can be added without modifying core strategy or risk logic, thanks to the unified gateway interface. |
| **Expanded asset universe** | Symbol configuration is data-driven; adding new assets requires only configuration changes (and cost model calibration). |
| **USDC support** | Quote currency abstraction exists in the data model; requires adding USDC-quoted symbol mappings and fee schedules. |
| **Active-active HA** | Requires distributed risk state (consensus protocol or shared state store), leader election for order placement, and split-brain protection. Significant architectural change. |
| **Machine learning signals** | Strategy Engine's modular design allows adding ML-based signal generators alongside rule-based ones, sharing the same risk and execution infrastructure. |
| **Web dashboard** | A read-only API layer can be added on top of the persistence layer and metrics store without touching the trading hot path. |

---

## Appendix A: Glossary

| Term | Definition |
|---|---|
| **Basis** | The price difference between a spot instrument and its corresponding perpetual futures contract. |
| **bps** | Basis points; 1 bps = 0.01%. |
| **Contango** | Market condition where futures price > spot price (positive basis). |
| **Backwardation** | Market condition where futures price < spot price (negative basis). |
| **Edge** | The expected or realized profit of a trade after all costs. |
| **Funding rate** | Periodic payment between long and short holders of a perpetual futures contract, designed to anchor the perp price to the spot price. |
| **Hot path** | The latency-critical sequence of operations from market data receipt to order acknowledgement. |
| **Kill switch** | Emergency mechanism to halt all trading activity and flatten positions. |
| **Leg** | A single order within a multi-order strategy cycle (e.g., a triangular arb has 3 legs). |
| **Mark-to-market** | Valuing positions at current market prices. |
| **Notional** | The total dollar value of a position (price × size). |
| **Slippage** | The difference between the expected execution price and the actual fill price. |
| **Triangular arbitrage** | Exploiting price inconsistencies across three related trading pairs to generate risk-free profit. |

## Appendix B: Configuration Reference

```yaml
# Example configuration structure (V1)

system:
  instance_id: "prod-01"
  trading_mode: "dry_run"  # "live", "dry_run", or "backtest"
  require_live_confirmation: true  # require --confirm-live flag for live mode
  log_level: "INFO"
  timezone: "UTC"

venues:
  nobitex:
    enabled: true
    ws_url: "wss://api.nobitex.ir/ws"
    rest_url: "https://api.nobitex.ir"
    rate_limits:
      order_place: { capacity: 10, refill_per_second: 5 }
      order_cancel: { capacity: 20, refill_per_second: 10 }
      public_data: { capacity: 30, refill_per_second: 15 }
    symbols:
      spot: ["BTC/USDT", "ETH/USDT", "SOL/USDT"]
      perp: ["BTCUSDT", "ETHUSDT", "SOLUSDT"]

  kcex:
    enabled: true
    ws_url: "wss://api.kcex.com/ws"
    rest_url: "https://api.kcex.com"
    rate_limits:
      order_place: { capacity: 15, refill_per_second: 7 }
      order_cancel: { capacity: 25, refill_per_second: 12 }
      public_data: { capacity: 40, refill_per_second: 20 }
    symbols:
      spot: ["BTC/USDT", "ETH/USDT", "SOL/USDT"]
      perp: ["BTCUSDT", "ETHUSDT", "SOLUSDT"]

strategies:
  triangular_arb:
    enabled: true
    min_edge_bps: 18
    fee_estimate_bps: 8
    slippage_buffer_bps: 5
    execution_risk_buffer_bps: 4
    fill_timeout_ms: 3000
    max_retries: 2

  basis_arb:
    enabled: true
    min_net_edge_bps: 22
    fee_estimate_bps: 10
    slippage_buffer_bps: 6
    funding_uncertainty_buffer_bps: 5
    transfer_cost_amortization_bps: 3
    fill_timeout_ms: 15000
    holding_horizon_hours: 168  # 1 week default

risk:
  max_position:
    BTC: 1.5
    ETH: 25
    SOL: 800
  max_notional_per_venue:
    nobitex: 250000
    kcex: 200000
  daily_loss_cap_usdt: 12500
  warning_threshold_pct: 80
  max_open_orders:
    global: 120
    per_venue: 70
    per_symbol: 20
  data_freshness:
    warning_ms: 500
    block_ms: 2000
  reconciliation:
    interval_seconds: 60
    mismatch_threshold_pct: 0.5
  checkpoint_interval_seconds: 5

cost_model:
  slippage_curve_lookback_fills: 500
  fee_tier_refresh_interval_seconds: 3600
  funding_rate_lookback_intervals: 12

monitoring:
  metrics:
    flush_interval_seconds: 10
    ingestion_delay_sla_seconds: 15
  alerting:
    delivery_delay_sla_seconds: 30
    p1_ack_sla_minutes: 5
    p1_mitigation_sla_minutes: 15
    channels: ["telegram", "pagerduty"]
  logging:
    availability_sla_pct: 99.9
    availability_window_minutes: 1

dry_run:
  initial_capital_usdt: 100000
  simulated_latency_ms: 50
  reject_rate_pct: 0.0
  use_live_slippage_model: true
  persist_to_separate_table: true

persistence:
  checkpoint_db: "./data/checkpoints.db"  # SQLite path (modernc.org/sqlite)
  cold_store_dsn: "postgres://user:pass@db-host:5432/trading?sslmode=require"  # pgx DSN
  cold_store_pool_size: 10
  trade_log_retention_days: 30
  metrics_retention:
    full_resolution_days: 90
    downsampled_years: 2

runtime:
  gomaxprocs: 0       # 0 = use all available cores
  gogc: 400           # reduce GC frequency on hot path
  gomemlimit: "2GiB"  # hard memory ceiling
```
