package marketdata

import (
	"sync/atomic"

	"github.com/crypto-trading/trading/internal/domain"
)

type TradeRingBuffer struct {
	trades []atomic.Pointer[domain.Trade]
	head   atomic.Uint64
	cap    uint64
}

func NewTradeRingBuffer(capacity int) *TradeRingBuffer {
	rb := &TradeRingBuffer{
		trades: make([]atomic.Pointer[domain.Trade], capacity),
		cap:    uint64(capacity),
	}
	return rb
}

func (rb *TradeRingBuffer) Push(trade *domain.Trade) {
	idx := rb.head.Add(1) - 1
	rb.trades[idx%rb.cap].Store(trade)
}

func (rb *TradeRingBuffer) Recent(n int) []*domain.Trade {
	head := rb.head.Load()
	if head == 0 {
		return nil
	}

	count := uint64(n)
	if count > rb.cap {
		count = rb.cap
	}
	if count > head {
		count = head
	}

	result := make([]*domain.Trade, 0, count)
	start := head - count
	for i := start; i < head; i++ {
		t := rb.trades[i%rb.cap].Load()
		if t != nil {
			result = append(result, t)
		}
	}
	return result
}

func (rb *TradeRingBuffer) Len() int {
	head := rb.head.Load()
	if head > rb.cap {
		return int(rb.cap)
	}
	return int(head)
}
