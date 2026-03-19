package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

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

func (s OrderStatus) IsTerminal() bool {
	return s == OrderStatusFilled || s == OrderStatusCancelled ||
		s == OrderStatusRejected || s == OrderStatusSubmitFailed
}

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

type TradingMode string

const (
	TradingModeLive    TradingMode = "live"
	TradingModeDryRun  TradingMode = "dry_run"
	TradingModeBacktest TradingMode = "backtest"
)

type EndpointCategory string

const (
	EndpointPublicData  EndpointCategory = "public_data"
	EndpointPrivateData EndpointCategory = "private_data"
	EndpointOrderPlace  EndpointCategory = "order_place"
	EndpointOrderCancel EndpointCategory = "order_cancel"
	EndpointAccount     EndpointCategory = "account"
)

type PriceLevel struct {
	Price decimal.Decimal
	Size  decimal.Decimal
}

type OrderBookSnapshot struct {
	Venue          string
	Symbol         string
	Bids           []PriceLevel
	Asks           []PriceLevel
	Sequence       uint64
	VenueTimestamp time.Time
	LocalTimestamp  time.Time
}

func (ob *OrderBookSnapshot) BestBid() (PriceLevel, bool) {
	if len(ob.Bids) == 0 {
		return PriceLevel{}, false
	}
	return ob.Bids[0], true
}

func (ob *OrderBookSnapshot) BestAsk() (PriceLevel, bool) {
	if len(ob.Asks) == 0 {
		return PriceLevel{}, false
	}
	return ob.Asks[0], true
}

func (ob *OrderBookSnapshot) MidPrice() (decimal.Decimal, bool) {
	bid, hasBid := ob.BestBid()
	ask, hasAsk := ob.BestAsk()
	if !hasBid || !hasAsk {
		return decimal.Zero, false
	}
	return bid.Price.Add(ask.Price).Div(decimal.NewFromInt(2)), true
}

type OrderBookDelta struct {
	Venue          string
	Symbol         string
	Bids           []PriceLevel
	Asks           []PriceLevel
	Sequence       uint64
	VenueTimestamp time.Time
	LocalTimestamp  time.Time
}

type Trade struct {
	Venue     string
	Symbol    string
	Price     decimal.Decimal
	Size      decimal.Decimal
	Side      Side
	Timestamp time.Time
	TradeID   string
}

type FundingRate struct {
	Venue     string
	Symbol    string
	Rate      decimal.Decimal
	Timestamp time.Time
	NextTime  time.Time
}

type CostEstimate struct {
	FeeBps      decimal.Decimal
	SlippageBps decimal.Decimal
	FundingBps  *decimal.Decimal
	TotalBps    decimal.Decimal
	Confidence  decimal.Decimal
}

type LegSpec struct {
	Symbol         string
	Side           Side
	InstrumentType InstrumentType
	Price          decimal.Decimal
	Size           decimal.Decimal
	OrderType      OrderType
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
	Size           decimal.Decimal
	EntryPrice     decimal.Decimal
	UnrealizedPnL  decimal.Decimal
	MarginUsed     decimal.Decimal
	UpdatedAt      time.Time
}

type Balance struct {
	Venue string
	Asset string
	Free  decimal.Decimal
	Locked decimal.Decimal
	Total  decimal.Decimal
}

type VenueAssetKey struct {
	Venue string
	Asset string
}

type OrderCountState struct {
	Global    int
	PerVenue  map[string]int
	PerSymbol map[string]int
}

type RiskState struct {
	Mode               RiskMode
	DailyRealizedPnL   decimal.Decimal
	DailyUnrealizedPnL decimal.Decimal
	Positions          map[VenueAssetKey]*Position
	OpenOrderCounts    OrderCountState
	VenueNotionals     map[string]decimal.Decimal
	LastCheckpoint     time.Time
	KillSwitchActive   bool
	KillSwitchReason   string
}

type OrderRequest struct {
	InternalID     uuid.UUID
	SignalID       uuid.UUID
	Venue          string
	Symbol         string
	Side           Side
	InstrumentType InstrumentType
	OrderType      OrderType
	Price          decimal.Decimal
	Size           decimal.Decimal
	IdempotencyKey string
}

type OrderAck struct {
	InternalID uuid.UUID
	VenueID    string
	Status     OrderStatus
	Timestamp  time.Time
}

type CancelAck struct {
	InternalID uuid.UUID
	VenueID    string
	Status     OrderStatus
	Timestamp  time.Time
}

type FeeTier struct {
	MakerFeeBps decimal.Decimal
	TakerFeeBps decimal.Decimal
	Venue       string
	UpdatedAt   time.Time
}

type OrderStateChange struct {
	Order      Order
	PrevStatus OrderStatus
	NewStatus  OrderStatus
	Timestamp  time.Time
}

type ExecutionReport struct {
	SignalID        uuid.UUID
	Strategy        StrategyType
	Venue           string
	Legs            []LegExecution
	ExpectedEdgeBps decimal.Decimal
	RealizedEdgeBps decimal.Decimal
	TotalFees       decimal.Decimal
	SlippageBps     decimal.Decimal
	Status          string
	StartedAt       time.Time
	CompletedAt     time.Time
}

type LegExecution struct {
	Symbol       string
	Side         Side
	ExpectedPrice decimal.Decimal
	ActualPrice   decimal.Decimal
	ExpectedSize  decimal.Decimal
	ActualSize    decimal.Decimal
	SlippageBps   decimal.Decimal
	Fee           decimal.Decimal
}

type FundingRegime string

const (
	FundingRegimeStable   FundingRegime = "STABLE"
	FundingRegimeVolatile FundingRegime = "VOLATILE"
)

type AlertSeverity string

const (
	AlertP1 AlertSeverity = "P1"
	AlertP2 AlertSeverity = "P2"
)
