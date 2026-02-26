package domain

import "github.com/shopspring/decimal"

func ParseDecimal(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// SymbolMappings maps internal canonical symbols to venue-specific symbols.
var NobitexSymbolMap = map[string]string{
	"BTC/USDT":  "BTCUSDT",
	"ETH/USDT":  "ETHUSDT",
	"SOL/USDT":  "SOLUSDT",
	"BTCUSDT":   "BTCUSDT_PERP",
	"ETHUSDT":   "ETHUSDT_PERP",
	"SOLUSDT":   "SOLUSDT_PERP",
}

var KCEXSymbolMap = map[string]string{
	"BTC/USDT":  "BTC_USDT",
	"ETH/USDT":  "ETH_USDT",
	"SOL/USDT":  "SOL_USDT",
	"BTCUSDT":   "BTCUSDT",
	"ETHUSDT":   "ETHUSDT",
	"SOLUSDT":   "SOLUSDT",
}

func MapSymbol(internal string, mapping map[string]string) string {
	if v, ok := mapping[internal]; ok {
		return v
	}
	return internal
}
