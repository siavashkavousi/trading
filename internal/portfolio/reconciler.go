package portfolio

import (
	"context"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

type Reconciler struct {
	manager    *Manager
	gateways   map[string]gateway.VenueGateway
	interval   time.Duration
	threshold  float64
	logger     *slog.Logger
	onMismatch func(venue string)
}

func NewReconciler(
	manager *Manager,
	gateways map[string]gateway.VenueGateway,
	interval time.Duration,
	threshold float64,
	logger *slog.Logger,
) *Reconciler {
	return &Reconciler{
		manager:   manager,
		gateways:  gateways,
		interval:  interval,
		threshold: threshold,
		logger:    logger,
	}
}

func (r *Reconciler) SetMismatchCallback(fn func(venue string)) {
	r.onMismatch = fn
}

func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *Reconciler) reconcileAll(ctx context.Context) {
	for name, gw := range r.gateways {
		r.reconcileVenue(ctx, name, gw)
	}
}

func (r *Reconciler) reconcileVenue(ctx context.Context, venue string, gw gateway.VenueGateway) {
	balances, err := gw.GetBalances(ctx)
	if err != nil {
		r.logger.Error("reconciliation: failed to get balances",
			"venue", venue, "error", err)
		return
	}

	for asset, venueBal := range balances {
		internalBal, ok := r.manager.GetBalance(venue, asset)
		if !ok {
			r.manager.UpdateBalance(venue, asset, venueBal.Free, venueBal.Locked)
			continue
		}

		if !internalBal.Total.IsZero() {
			diff := venueBal.Total.Sub(internalBal.Total).Abs()
			pct := diff.Div(internalBal.Total).Mul(decimal.NewFromInt(100))

			if pct.GreaterThan(decimal.NewFromFloat(r.threshold)) {
				r.logger.Error("reconciliation mismatch detected",
					"venue", venue,
					"asset", asset,
					"internal", internalBal.Total.String(),
					"venue_actual", venueBal.Total.String(),
					"diff_pct", pct.String(),
				)

				if r.onMismatch != nil {
					r.onMismatch(venue)
				}
			}
		}

		r.manager.UpdateBalance(venue, asset, venueBal.Free, venueBal.Locked)
	}

	positions, err := gw.GetPositions(ctx)
	if err != nil {
		r.logger.Error("reconciliation: failed to get positions",
			"venue", venue, "error", err)
		return
	}

	for _, venuePos := range positions {
		internalPos, ok := r.manager.GetPosition(venue, venuePos.Asset)
		if !ok {
			r.manager.UpdatePosition(venuePos)
			continue
		}

		if !internalPos.Size.IsZero() {
			diff := venuePos.Size.Sub(internalPos.Size).Abs()
			pct := diff.Div(internalPos.Size.Abs()).Mul(decimal.NewFromInt(100))

			if pct.GreaterThan(decimal.NewFromFloat(r.threshold)) {
				r.logger.Error("position reconciliation mismatch",
					"venue", venue,
					"asset", venuePos.Asset,
					"internal_size", internalPos.Size.String(),
					"venue_size", venuePos.Size.String(),
					"diff_pct", pct.String(),
				)

				if r.onMismatch != nil {
					r.onMismatch(venue)
				}
			}
		}

		r.manager.UpdatePosition(domain.Position{
			Venue:          venue,
			Asset:          venuePos.Asset,
			InstrumentType: venuePos.InstrumentType,
			Size:           venuePos.Size,
			EntryPrice:     venuePos.EntryPrice,
			UnrealizedPnL:  venuePos.UnrealizedPnL,
			MarginUsed:     venuePos.MarginUsed,
			UpdatedAt:      time.Now(),
		})
	}

	r.logger.Debug("reconciliation completed", "venue", venue)
}
