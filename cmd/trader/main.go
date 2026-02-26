package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/config"
	"github.com/crypto-trading/trading/internal/costmodel"
	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
	"github.com/crypto-trading/trading/internal/execution"
	"github.com/crypto-trading/trading/internal/gateway"
	"github.com/crypto-trading/trading/internal/gateway/kcex"
	"github.com/crypto-trading/trading/internal/gateway/nobitex"
	"github.com/crypto-trading/trading/internal/gateway/simulated"
	"github.com/crypto-trading/trading/internal/marketdata"
	"github.com/crypto-trading/trading/internal/monitor"
	"github.com/crypto-trading/trading/internal/order"
	"github.com/crypto-trading/trading/internal/persistence"
	"github.com/crypto-trading/trading/internal/portfolio"
	"github.com/crypto-trading/trading/internal/risk"
	"github.com/crypto-trading/trading/internal/strategy"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "Path to configuration file")
	confirmLive := flag.Bool("confirm-live", false, "Confirm live trading mode")
	flag.Parse()

	logger := initLogger("INFO")

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger = initLogger(cfg.System.LogLevel)
	logger.Info("configuration loaded",
		"instance_id", cfg.System.InstanceID,
		"trading_mode", cfg.System.TradingMode,
	)

	tradingMode := domain.TradingMode(cfg.System.TradingMode)
	if tradingMode == domain.TradingModeLive {
		if cfg.System.RequireLiveConfirmation && !*confirmLive {
			logger.Error("LIVE TRADING requires --confirm-live flag")
			os.Exit(1)
		}
		logger.Warn("=== LIVE TRADING ACTIVE ===")
	} else {
		logger.Info("running in mode", "mode", cfg.System.TradingMode)
	}

	configureRuntime(cfg.Runtime, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	reg := prometheus.DefaultRegisterer
	metrics := monitor.NewMetrics(reg)
	_ = metrics

	tracerShutdown, err := monitor.InitTracer(cfg.System.InstanceID, logger)
	if err != nil {
		logger.Warn("failed to initialize tracer", "error", err)
	}

	alertMgr := monitor.NewAlertManager(cfg.Monitoring.Alerting.Channels, logger)

	bus := eventbus.New(1024, logger)

	sqliteStore, err := persistence.NewSQLiteStore(cfg.Persistence.CheckpointDB, logger)
	if err != nil {
		logger.Error("failed to initialize SQLite store", "error", err)
		os.Exit(1)
	}
	defer sqliteStore.Close()

	var pgStore *persistence.PostgresStore
	if cfg.Persistence.ColdStoreDSN != "" {
		pgStore, err = persistence.NewPostgresStore(ctx, cfg.Persistence.ColdStoreDSN, cfg.Persistence.ColdStorePoolSize, logger)
		if err != nil {
			logger.Warn("PostgreSQL cold store unavailable, continuing without it", "error", err)
		} else if pgStore != nil {
			defer pgStore.Close()
			if err := pgStore.RunMigrations(ctx); err != nil {
				logger.Error("failed to run PostgreSQL migrations", "error", err)
			}
		}
	}

	asyncWriter := persistence.NewAsyncWriter(sqliteStore, pgStore, 10000, logger)
	asyncWriter.Run()

	mdService := marketdata.NewService(
		bus,
		cfg.Risk.DataFreshness.WarningDuration(),
		cfg.Risk.DataFreshness.BlockDuration(),
		logger,
	)

	gateways := buildGateways(cfg, mdService, tradingMode, logger)

	costSvc := costmodel.NewService(
		gateways,
		cfg.CostModel.FeeTierRefreshInterval(),
		cfg.CostModel.FundingRateLookbackIntervals,
		logger,
	)

	riskMgr := risk.NewManager(
		&cfg.Risk,
		mdService,
		"data/killswitch.json",
		logger,
	)

	orderMgr := order.NewManager(gateways, bus, logger)

	execEngine := execution.NewEngine(
		orderMgr,
		riskMgr,
		bus,
		cfg.Strategies.TriangularArb.FillTimeout(),
		cfg.Strategies.BasisArb.FillTimeout(),
		cfg.Strategies.TriangularArb.MaxRetries,
		logger,
	)

	riskMgr.SetKillSwitchCallback(execEngine.KillSwitchHandler(ctx))

	portfolioMgr := portfolio.NewManager(mdService, cfg.System.TradingMode, logger)

	reconciler := portfolio.NewReconciler(
		portfolioMgr,
		gateways,
		cfg.Risk.Reconciliation.Interval(),
		cfg.Risk.Reconciliation.MismatchThresholdPct,
		logger,
	)
	reconciler.SetMismatchCallback(func(venue string) {
		alertMgr.Fire(monitor.AlertLevelP1, "reconciliation_mismatch",
			fmt.Sprintf("position diff > %.1f%% on %s", cfg.Risk.Reconciliation.MismatchThresholdPct, venue),
			fmt.Sprintf("Trading blocked for venue %s until resolved", venue))
	})

	stratEngine := strategy.NewEngine(bus, logger)

	if cfg.Strategies.TriangularArb.Enabled {
		for venueName := range gateways {
			paths := strategy.DefaultTriangularPaths(venueName)
			triMod := strategy.NewTriArbModule(
				venueName,
				paths,
				costSvc,
				bus,
				cfg.Strategies.TriangularArb.MinEdgeBps,
				logger,
			)
			stratEngine.RegisterModule(triMod)
		}
	}

	if cfg.Strategies.BasisArb.Enabled {
		venues := make([]string, 0)
		for v := range gateways {
			venues = append(venues, v)
		}
		basisMod := strategy.NewBasisArbModule(
			venues,
			[]string{"BTC", "ETH", "SOL"},
			costSvc,
			bus,
			cfg.Strategies.BasisArb.MinNetEdgeBps,
			cfg.Strategies.BasisArb.HoldingHorizonHours,
			logger,
		)
		stratEngine.RegisterModule(basisMod)
	}

	if riskMgr.IsKillSwitchActive() {
		logger.Warn("KILL SWITCH IS ACTIVE - system will remain halted until manually resumed")
	}

	for name, gw := range gateways {
		if err := gw.Connect(ctx); err != nil {
			logger.Error("failed to connect to venue", "venue", name, "error", err)
			os.Exit(1)
		}
		logger.Info("venue connected", "venue", name)
	}

	go costSvc.RunFeeTierRefresher(ctx)
	go mdService.RunHeartbeatMonitor(ctx)
	go riskMgr.RunPeriodicCheck(ctx)
	go reconciler.Run(ctx)
	go stratEngine.Run(ctx)
	go execEngine.Run(ctx)

	go runCheckpointer(ctx, riskMgr, asyncWriter, cfg.Risk.CheckpointInterval(), logger)

	go startMetricsServer(logger)

	if err := config.WatchAndReload(*configPath, func(newCfg *config.Config) {
		logger.Info("configuration reloaded")
	}); err != nil {
		logger.Warn("config hot-reload setup failed", "error", err)
	}

	logger.Info("system started successfully",
		"instance_id", cfg.System.InstanceID,
		"trading_mode", cfg.System.TradingMode,
		"venues", len(gateways),
	)

	select {
	case sig := <-sigCh:
		logger.Info("received shutdown signal", "signal", sig)
	case <-ctx.Done():
	}

	logger.Info("shutting down...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	orderMgr.CancelAllOrders(shutdownCtx)

	for name, gw := range gateways {
		if err := gw.Close(); err != nil {
			logger.Error("failed to close venue gateway", "venue", name, "error", err)
		}
	}

	bus.Close()
	asyncWriter.Stop()

	if tracerShutdown != nil {
		if err := tracerShutdown(shutdownCtx); err != nil {
			logger.Error("failed to shut down tracer", "error", err)
		}
	}

	logger.Info("shutdown complete")
}

func initLogger(level string) *slog.Logger {
	var logLevel slog.Level
	switch level {
	case "DEBUG":
		logLevel = slog.LevelDebug
	case "INFO":
		logLevel = slog.LevelInfo
	case "WARN":
		logLevel = slog.LevelWarn
	case "ERROR":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func configureRuntime(cfg config.RuntimeConfig, logger *slog.Logger) {
	if cfg.GoMaxProcs > 0 {
		runtime.GOMAXPROCS(cfg.GoMaxProcs)
	}
	logger.Info("runtime configured",
		"GOMAXPROCS", runtime.GOMAXPROCS(0),
		"GOGC", cfg.GOGC,
		"GOMEMLIMIT", cfg.GoMemLimit,
	)

	if cfg.GOGC > 0 {
		debug.SetGCPercent(cfg.GOGC)
	}
}

func buildGateways(cfg *config.Config, mdService *marketdata.Service, mode domain.TradingMode, logger *slog.Logger) map[string]gateway.VenueGateway {
	gateways := make(map[string]gateway.VenueGateway)

	if mode == domain.TradingModeDryRun {
		for venueName, venueCfg := range cfg.Venues {
			if !venueCfg.Enabled {
				continue
			}

			fillSim := simulated.NewFillSimulator(
				cfg.DryRun.SimulatedLatencyMs,
				cfg.DryRun.RejectRatePct,
				decimal.NewFromFloat(2),
				decimal.NewFromFloat(5),
			)

			gw := simulated.New(
				venueName,
				fillSim,
				mdService,
				cfg.DryRun.InitialCapitalUSDT,
				cfg.DryRun.SimulatedLatencyMs,
				logger,
			)
			gateways[venueName] = gw
		}
		return gateways
	}

	for venueName, venueCfg := range cfg.Venues {
		if !venueCfg.Enabled {
			continue
		}

		apiKey := os.Getenv(fmt.Sprintf("%s_API_KEY", venueName))
		apiSecret := os.Getenv(fmt.Sprintf("%s_API_SECRET", venueName))

		switch venueName {
		case "nobitex":
			gw := nobitex.New(venueCfg.WsURL, venueCfg.RestURL, apiKey, apiSecret, logger)
			gateways[venueName] = gw
		case "kcex":
			gw := kcex.New(venueCfg.WsURL, venueCfg.RestURL, apiKey, apiSecret, logger)
			gateways[venueName] = gw
		default:
			logger.Warn("unknown venue, skipping", "venue", venueName)
		}
	}

	return gateways
}

func runCheckpointer(ctx context.Context, riskMgr *risk.Manager, writer *persistence.AsyncWriter, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := riskMgr.GetCheckpointState()
			writer.Write(persistence.WriteRequest{
				Type:    persistence.WriteTypeRiskCheckpoint,
				Payload: state,
			})
			logger.Debug("risk state checkpointed")
		}
	}
}

func startMetricsServer(logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", monitor.MetricsHandler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:    ":9090",
		Handler: mux,
	}

	logger.Info("metrics server starting", "addr", ":9090")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("metrics server error", "error", err)
	}
}
