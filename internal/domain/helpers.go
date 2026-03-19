package domain

import (
	"strings"

	"github.com/shopspring/decimal"
)

func ParseDecimal(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// NobitexOrderBookSymbolMap maps internal symbols to Nobitex orderbook symbols.
// Nobitex orderbook endpoint uses concatenated uppercase symbols.
var NobitexOrderBookSymbolMap = map[string]string{
	"BTC/USDT": "BTCUSDT",
	"ETH/USDT": "ETHUSDT",
	"SOL/USDT": "SOLUSDT",
	"BTC/IRT":  "BTCIRT",
	"ETH/IRT":  "ETHIRT",
	"USDT/IRT": "USDTIRT",
}

// NobitexCurrencyPair maps internal symbols to Nobitex srcCurrency/dstCurrency pairs
// used in order placement and listing. Nobitex uses lowercase currency codes.
type CurrencyPair struct {
	Src string
	Dst string
}

var NobitexCurrencyPairMap = map[string]CurrencyPair{
	"BTC/USDT": {Src: "btc", Dst: "usdt"},
	"ETH/USDT": {Src: "eth", Dst: "usdt"},
	"SOL/USDT": {Src: "sol", Dst: "usdt"},
	"BTC/IRT":  {Src: "btc", Dst: "rls"},
	"ETH/IRT":  {Src: "eth", Dst: "rls"},
	"USDT/IRT": {Src: "usdt", Dst: "rls"},
}

// KCEXSpotSymbolMap maps internal symbols to KCEX spot symbols (dash-separated).
var KCEXSpotSymbolMap = map[string]string{
	"BTC/USDT": "BTC-USDT",
	"ETH/USDT": "ETH-USDT",
	"SOL/USDT": "SOL-USDT",
}

// KCEXFuturesSymbolMap maps internal perp symbols to KCEX futures symbols.
var KCEXFuturesSymbolMap = map[string]string{
	"BTCUSDT": "BTCUSDTM",
	"ETHUSDT": "ETHUSDTM",
	"SOLUSDT": "SOLUSDTM",
}

// MapSymbol maps an internal symbol to a venue-specific symbol using the provided map.
func MapSymbol(internal string, mapping map[string]string) string {
	if v, ok := mapping[internal]; ok {
		return v
	}
	return internal
}

// MapNobitexCurrencyPair returns the srcCurrency/dstCurrency pair for Nobitex orders.
func MapNobitexCurrencyPair(internal string) (src, dst string) {
	if p, ok := NobitexCurrencyPairMap[internal]; ok {
		return p.Src, p.Dst
	}
	parts := strings.SplitN(internal, "/", 2)
	if len(parts) == 2 {
		return strings.ToLower(parts[0]), strings.ToLower(parts[1])
	}
	return strings.ToLower(internal), "usdt"
}

// IsKCEXFutures returns true if the internal symbol is a futures/perp symbol.
func IsKCEXFutures(internal string) bool {
	_, ok := KCEXFuturesSymbolMap[internal]
	return ok
}

// MapKCEXSymbol maps an internal symbol to the correct KCEX symbol,
// automatically detecting whether it's spot or futures.
func MapKCEXSymbol(internal string) string {
	if v, ok := KCEXFuturesSymbolMap[internal]; ok {
		return v
	}
	if v, ok := KCEXSpotSymbolMap[internal]; ok {
		return v
	}
	return internal
}
