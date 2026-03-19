package simulated

import (
	"math/rand"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
)

type FillSimulator interface {
	SimulateFill(order domain.OrderRequest, book *domain.OrderBookSnapshot) (*SimulatedFill, error)
}

type SimulatedFill struct {
	FillPrice decimal.Decimal
	FillSize  decimal.Decimal
	Fee       decimal.Decimal
	LatencyMs int
	Status    domain.OrderStatus
}

type DefaultFillSimulator struct {
	latencyMs     int
	rejectRatePct float64
	makerFeeBps   decimal.Decimal
	takerFeeBps   decimal.Decimal
	rng           *rand.Rand
}

func NewFillSimulator(latencyMs int, rejectRatePct float64, makerFeeBps, takerFeeBps decimal.Decimal) *DefaultFillSimulator {
	return &DefaultFillSimulator{
		latencyMs:     latencyMs,
		rejectRatePct: rejectRatePct,
		makerFeeBps:   makerFeeBps,
		takerFeeBps:   takerFeeBps,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *DefaultFillSimulator) SimulateFill(order domain.OrderRequest, book *domain.OrderBookSnapshot) (*SimulatedFill, error) {
	if s.rejectRatePct > 0 && s.rng.Float64()*100 < s.rejectRatePct {
		return &SimulatedFill{
			Status:    domain.OrderStatusRejected,
			LatencyMs: s.latencyMs,
		}, nil
	}

	if book == nil {
		return &SimulatedFill{
			Status:    domain.OrderStatusRejected,
			LatencyMs: s.latencyMs,
		}, nil
	}

	var fillPrice decimal.Decimal
	var fillSize decimal.Decimal
	var feeBps decimal.Decimal

	switch order.OrderType {
	case domain.OrderTypeMarket:
		feeBps = s.takerFeeBps
		if order.Side == domain.SideBuy {
			if len(book.Asks) == 0 {
				return &SimulatedFill{Status: domain.OrderStatusRejected, LatencyMs: s.latencyMs}, nil
			}
			fillPrice, fillSize = simulateMarketFill(book.Asks, order.Size)
		} else {
			if len(book.Bids) == 0 {
				return &SimulatedFill{Status: domain.OrderStatusRejected, LatencyMs: s.latencyMs}, nil
			}
			fillPrice, fillSize = simulateMarketFill(book.Bids, order.Size)
		}

	case domain.OrderTypeLimit:
		feeBps = s.makerFeeBps
		if order.Side == domain.SideBuy {
			if len(book.Asks) == 0 {
				return &SimulatedFill{Status: domain.OrderStatusRejected, LatencyMs: s.latencyMs}, nil
			}
			bestAsk := book.Asks[0].Price
			if order.Price.LessThan(bestAsk) {
				return &SimulatedFill{
					FillPrice: order.Price,
					FillSize:  decimal.Zero,
					Status:    domain.OrderStatusAcknowledged,
					LatencyMs: s.latencyMs,
				}, nil
			}
			fillPrice, fillSize = simulateMarketFill(book.Asks, order.Size)
		} else {
			if len(book.Bids) == 0 {
				return &SimulatedFill{Status: domain.OrderStatusRejected, LatencyMs: s.latencyMs}, nil
			}
			bestBid := book.Bids[0].Price
			if order.Price.GreaterThan(bestBid) {
				return &SimulatedFill{
					FillPrice: order.Price,
					FillSize:  decimal.Zero,
					Status:    domain.OrderStatusAcknowledged,
					LatencyMs: s.latencyMs,
				}, nil
			}
			fillPrice, fillSize = simulateMarketFill(book.Bids, order.Size)
		}
	}

	fee := fillPrice.Mul(fillSize).Mul(feeBps).Div(decimal.NewFromInt(10000))

	status := domain.OrderStatusFilled
	if fillSize.LessThan(order.Size) {
		status = domain.OrderStatusPartialFill
	}

	return &SimulatedFill{
		FillPrice: fillPrice,
		FillSize:  fillSize,
		Fee:       fee,
		LatencyMs: s.latencyMs,
		Status:    status,
	}, nil
}

func simulateMarketFill(levels []domain.PriceLevel, size decimal.Decimal) (decimal.Decimal, decimal.Decimal) {
	remaining := size
	totalCost := decimal.Zero
	totalFilled := decimal.Zero

	for _, level := range levels {
		if remaining.IsZero() {
			break
		}
		fillAtLevel := decimal.Min(remaining, level.Size)
		totalCost = totalCost.Add(fillAtLevel.Mul(level.Price))
		totalFilled = totalFilled.Add(fillAtLevel)
		remaining = remaining.Sub(fillAtLevel)
	}

	if totalFilled.IsZero() {
		return decimal.Zero, decimal.Zero
	}

	avgPrice := totalCost.Div(totalFilled)
	return avgPrice, totalFilled
}
