package domain

import "github.com/shopspring/decimal"

const PricePrecision = 1_000_000_000 // 9 decimal places (nano-units)

var pricePrecisionDec = decimal.NewFromInt(PricePrecision)

type FixedPrice int64

func ToFixed(d decimal.Decimal) FixedPrice {
	return FixedPrice(d.Mul(pricePrecisionDec).IntPart())
}

func (f FixedPrice) ToDecimal() decimal.Decimal {
	return decimal.New(int64(f), -9)
}

func (f FixedPrice) Add(other FixedPrice) FixedPrice {
	return f + other
}

func (f FixedPrice) Sub(other FixedPrice) FixedPrice {
	return f - other
}

func (f FixedPrice) Mul(other FixedPrice) FixedPrice {
	return FixedPrice(int64(f) * int64(other) / PricePrecision)
}

func (f FixedPrice) Div(other FixedPrice) FixedPrice {
	if other == 0 {
		return 0
	}
	return FixedPrice(int64(f) * PricePrecision / int64(other))
}

func (f FixedPrice) GT(other FixedPrice) bool  { return f > other }
func (f FixedPrice) GTE(other FixedPrice) bool { return f >= other }
func (f FixedPrice) LT(other FixedPrice) bool  { return f < other }
func (f FixedPrice) LTE(other FixedPrice) bool { return f <= other }

func FixedFromBps(bps int64) FixedPrice {
	return FixedPrice(bps * PricePrecision / 10000)
}
