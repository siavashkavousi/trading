package simulated

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
)

func TestFillSimulator_MarketBuy(t *testing.T) {
	sim := NewFillSimulator(0, 0, decimal.NewFromFloat(2), decimal.NewFromFloat(5))

	book := &domain.OrderBookSnapshot{
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(1.0)},
			{Price: decimal.NewFromInt(50100), Size: decimal.NewFromFloat(2.0)},
		},
	}

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeMarket,
		Price:      decimal.NewFromInt(50000),
		Size:       decimal.NewFromFloat(0.5),
	}

	fill, err := sim.SimulateFill(req, book)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fill.Status != domain.OrderStatusFilled {
		t.Errorf("expected FILLED, got %s", fill.Status)
	}

	if !fill.FillSize.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("expected fill size 0.5, got %s", fill.FillSize)
	}

	if !fill.FillPrice.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("expected fill price 50000, got %s", fill.FillPrice)
	}

	if fill.Fee.IsZero() {
		t.Error("expected non-zero fee")
	}
}

func TestFillSimulator_MarketSell(t *testing.T) {
	sim := NewFillSimulator(0, 0, decimal.NewFromFloat(2), decimal.NewFromFloat(5))

	book := &domain.OrderBookSnapshot{
		Bids: []domain.PriceLevel{
			{Price: decimal.NewFromInt(49900), Size: decimal.NewFromFloat(3.0)},
		},
	}

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideSell,
		OrderType:  domain.OrderTypeMarket,
		Size:       decimal.NewFromFloat(1.0),
	}

	fill, err := sim.SimulateFill(req, book)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fill.Status != domain.OrderStatusFilled {
		t.Errorf("expected FILLED, got %s", fill.Status)
	}
}

func TestFillSimulator_PartialFill(t *testing.T) {
	sim := NewFillSimulator(0, 0, decimal.NewFromFloat(2), decimal.NewFromFloat(5))

	book := &domain.OrderBookSnapshot{
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(0.3)},
		},
	}

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeMarket,
		Size:       decimal.NewFromFloat(1.0),
	}

	fill, err := sim.SimulateFill(req, book)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fill.Status != domain.OrderStatusPartialFill {
		t.Errorf("expected PARTIAL_FILL, got %s", fill.Status)
	}

	if !fill.FillSize.Equal(decimal.NewFromFloat(0.3)) {
		t.Errorf("expected fill size 0.3, got %s", fill.FillSize)
	}
}

func TestFillSimulator_Rejection(t *testing.T) {
	sim := NewFillSimulator(0, 100, decimal.NewFromFloat(2), decimal.NewFromFloat(5))

	book := &domain.OrderBookSnapshot{
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(1.0)},
		},
	}

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeMarket,
		Size:       decimal.NewFromFloat(0.5),
	}

	fill, err := sim.SimulateFill(req, book)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fill.Status != domain.OrderStatusRejected {
		t.Errorf("expected REJECTED with 100%% reject rate, got %s", fill.Status)
	}
}

func TestFillSimulator_NilBook(t *testing.T) {
	sim := NewFillSimulator(0, 0, decimal.NewFromFloat(2), decimal.NewFromFloat(5))

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeMarket,
		Size:       decimal.NewFromFloat(0.5),
	}

	fill, err := sim.SimulateFill(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fill.Status != domain.OrderStatusRejected {
		t.Errorf("expected REJECTED with nil book, got %s", fill.Status)
	}
}
