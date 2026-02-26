package config

import (
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

var globalConfig atomic.Pointer[Config]

func Get() *Config {
	return globalConfig.Load()
}

func Load(configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")
	v.AutomaticEnv()

	v.SetDefault("system.log_level", "INFO")
	v.SetDefault("system.timezone", "UTC")
	v.SetDefault("system.require_live_confirmation", true)
	v.SetDefault("runtime.gomaxprocs", 0)
	v.SetDefault("runtime.gogc", 400)
	v.SetDefault("runtime.gomemlimit", "2GiB")
	v.SetDefault("persistence.cold_store_pool_size", 10)
	v.SetDefault("persistence.trade_log_retention_days", 30)
	v.SetDefault("dry_run.initial_capital_usdt", 100000)
	v.SetDefault("dry_run.simulated_latency_ms", 50)
	v.SetDefault("dry_run.reject_rate_pct", 0.0)
	v.SetDefault("dry_run.use_live_slippage_model", true)
	v.SetDefault("dry_run.persist_to_separate_table", true)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	validate := validator.New()
	if err := validate.Struct(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	globalConfig.Store(&cfg)
	return &cfg, nil
}

func WatchAndReload(configPath string, onChange func(*Config)) error {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config for watch: %w", err)
	}

	v.WatchConfig()
	v.OnConfigChange(func(_ fsnotify.Event) {
		var newCfg Config
		if err := v.Unmarshal(&newCfg); err != nil {
			slog.Error("failed to unmarshal reloaded config", "error", err)
			return
		}

		validate := validator.New()
		if err := validate.Struct(&newCfg); err != nil {
			slog.Error("reloaded config validation failed", "error", err)
			return
		}

		old := globalConfig.Load()
		globalConfig.Store(&newCfg)
		slog.Info("configuration reloaded successfully")

		if onChange != nil {
			onChange(&newCfg)
		}

		logConfigChanges(old, &newCfg)
	})

	return nil
}

func logConfigChanges(old, new *Config) {
	if old == nil || new == nil {
		return
	}
	if old.System.TradingMode != new.System.TradingMode {
		slog.Warn("trading mode changed",
			"old", old.System.TradingMode,
			"new", new.System.TradingMode,
		)
	}
	if old.System.LogLevel != new.System.LogLevel {
		slog.Info("log level changed",
			"old", old.System.LogLevel,
			"new", new.System.LogLevel,
		)
	}
}
