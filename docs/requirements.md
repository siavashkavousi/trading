# Strategy

For V1, the supported strategy set will include:

1. **Triangular arbitrage (single venue, spot)**
   - Trade three related spot pairs on the same exchange to capture temporary price inconsistencies.
   - Selected for lower operational complexity in V1 (no cross-venue transfer dependency for each cycle).

2. **Cross-market basis arbitrage (spot-perp, same venue)**
   - Hedge spot exposure with perpetual futures on the same exchange when basis/funding and spread conditions are favorable.
   - Selected to diversify opportunity sources while avoiding immediate multi-venue settlement risk in V1.

Spatial (cross-venue) arbitrage is out of V1 scope and can be considered in a later release once transfer automation and treasury routing are production-hardened.

# Venues

## Target exchanges (V1)
- **Nobitex**
- **KCEX**

## Instrument coverage
- **Spot**: enabled on both venues for base inventory and triangular paths.
- **Perpetual futures (USDT-margined)**: enabled on both venues for basis/funding opportunities.

## Initial symbol universe
- **Core assets**: BTC, ETH, SOL
- **Spot symbols**: BTC/USDT, ETH/USDT, SOL/USDT
- **Perp symbols**: BTCUSDT, ETHUSDT, SOLUSDT

## Quote currencies
- **USDT** (primary quote and collateral currency in V1)
- Optional later expansion: USDC pairs/markets once liquidity and operational support are validated.

# Risk Limits

The following hard limits apply globally unless overridden by stricter venue-specific constraints:

## Max position per asset (net exposure)
- BTC: **<= 1.5 BTC**
- ETH: **<= 25 ETH**
- SOL: **<= 800 SOL**

## Max notional per venue (gross)
- Nobitex: **<= 250,000 USDT**
- KCEX: **<= 200,000 USDT**

## Daily loss cap
- Portfolio-level realized + unrealized PnL stop: **-12,500 USDT/day**
- On breach: cancel all open orders, reduce to flat/hedged baseline, and require manual resume.

## Max open orders
- Global concurrent open orders: **<= 120**
- Per venue concurrent open orders: **<= 70**
- Per symbol concurrent open orders: **<= 20**

# Execution

## Profitability thresholds
Trades must satisfy all-in expected edge after costs before order placement.

### 1) Triangular arbitrage
Require:

`Expected cycle edge (bps) >= fees + slippage buffer + execution risk buffer`

Minimum threshold (V1 default):
- **>= 18 bps** gross observed discrepancy, with modeled costs:
  - Fees: 6-10 bps (maker/taker mix over 3 legs)
  - Slippage: 4-6 bps combined
  - Execution risk buffer: 3-5 bps

### 2) Cross-market basis arbitrage (spot-perp)
Require:

`Expected annualized basis/funding capture over holding horizon >= trading fees + slippage + funding uncertainty buffer + transfer/withdrawal amortization`

Minimum threshold (V1 default):
- **Net expected edge >= 22 bps** on entry+exit cycle equivalent, where model includes:
  - Spot + perp trading fees: 8-12 bps total round-trip
  - Slippage: 4-8 bps total
  - Funding impact (expected adverse case): 3-6 bps equivalent
  - Withdrawal/transfer cost amortization: 1-4 bps equivalent

## Cost model requirements
- Use venue-tier-specific fee schedules and account for maker/taker probability.
- Include symbol-specific slippage curves by order size bucket.
- Recompute thresholds when fee tier, funding regime, or transfer costs change materially.
- Block execution if required market data freshness SLA is violated.

# Monitoring

## Latency target
- **Market data to decision latency (p95): <= 60 ms**
- **Decision to order acknowledgement (p95): <= 120 ms**
- **End-to-end tick-to-ack (p95): <= 180 ms**

## Uptime target
- **Strategy engine monthly uptime: >= 99.5%**
- **Order routing service monthly uptime: >= 99.9%**

## Observability SLAs
- Metrics ingestion delay (p95): **<= 15 seconds**
- Critical alert delivery delay (p95): **<= 30 seconds**
- Log availability in centralized store: **>= 99.9% within 1 minute**
- Incident response:
  - P1 acknowledgement: **<= 5 minutes**
  - P1 mitigation action started: **<= 15 minutes**

## Required telemetry
- Per-strategy realized vs expected edge.
- Fill quality (slippage vs model) by symbol/venue.
- Funding paid/received and basis capture decomposition.
- Risk limit utilization and breach events.
- Order reject/cancel ratios and venue error codes.
