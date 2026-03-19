package domain

import "testing"

func TestMapNobitexCurrencyPair(t *testing.T) {
	tests := []struct {
		internal string
		wantSrc  string
		wantDst  string
	}{
		{"BTC/USDT", "btc", "usdt"},
		{"ETH/USDT", "eth", "usdt"},
		{"SOL/USDT", "sol", "usdt"},
		{"BTC/IRT", "btc", "rls"},
		{"USDT/IRT", "usdt", "rls"},
		{"UNKNOWN/PAIR", "unknown", "pair"},
	}

	for _, tt := range tests {
		src, dst := MapNobitexCurrencyPair(tt.internal)
		if src != tt.wantSrc || dst != tt.wantDst {
			t.Errorf("MapNobitexCurrencyPair(%q) = (%q, %q), want (%q, %q)",
				tt.internal, src, dst, tt.wantSrc, tt.wantDst)
		}
	}
}

func TestMapKCEXSymbol(t *testing.T) {
	tests := []struct {
		internal string
		want     string
	}{
		{"BTC/USDT", "BTC-USDT"},
		{"ETH/USDT", "ETH-USDT"},
		{"SOL/USDT", "SOL-USDT"},
		{"BTCUSDT", "BTCUSDTM"},
		{"ETHUSDT", "ETHUSDTM"},
		{"SOLUSDT", "SOLUSDTM"},
		{"UNKNOWN", "UNKNOWN"},
	}

	for _, tt := range tests {
		got := MapKCEXSymbol(tt.internal)
		if got != tt.want {
			t.Errorf("MapKCEXSymbol(%q) = %q, want %q", tt.internal, got, tt.want)
		}
	}
}

func TestIsKCEXFutures(t *testing.T) {
	if !IsKCEXFutures("BTCUSDT") {
		t.Error("expected BTCUSDT to be detected as futures")
	}
	if IsKCEXFutures("BTC/USDT") {
		t.Error("expected BTC/USDT to NOT be detected as futures")
	}
	if IsKCEXFutures("UNKNOWN") {
		t.Error("expected UNKNOWN to NOT be detected as futures")
	}
}

func TestNobitexOrderBookSymbolMap(t *testing.T) {
	tests := []struct {
		internal string
		want     string
	}{
		{"BTC/USDT", "BTCUSDT"},
		{"ETH/USDT", "ETHUSDT"},
		{"SOL/USDT", "SOLUSDT"},
		{"BTC/IRT", "BTCIRT"},
		{"USDT/IRT", "USDTIRT"},
	}

	for _, tt := range tests {
		got := MapSymbol(tt.internal, NobitexOrderBookSymbolMap)
		if got != tt.want {
			t.Errorf("MapSymbol(%q, NobitexOrderBookSymbolMap) = %q, want %q", tt.internal, got, tt.want)
		}
	}
}
