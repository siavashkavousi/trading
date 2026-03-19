package domain

import "testing"

func TestExtractAsset(t *testing.T) {
	tests := []struct {
		symbol string
		want   string
	}{
		{"BTC/USDT", "BTC"},
		{"ETH/USDT", "ETH"},
		{"SOL/USDT", "SOL"},
		{"BTC/IRT", "BTC"},
		{"BTCUSDT", "BTC"},
		{"ETHUSDT", "ETH"},
		{"SOLUSDT", "SOL"},
		{"UNKNOWN", "UNKNOWN"},
		{"XRP/USDT", "XRP"},
	}

	for _, tt := range tests {
		got := ExtractAsset(tt.symbol)
		if got != tt.want {
			t.Errorf("ExtractAsset(%q) = %q, want %q", tt.symbol, got, tt.want)
		}
	}
}
