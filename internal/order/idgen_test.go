package order

import "testing"

func TestNewOrderID(t *testing.T) {
	id1 := NewOrderID()
	id2 := NewOrderID()

	if id1 == id2 {
		t.Error("expected unique IDs")
	}

	var zeroUUID [16]byte
	if id1 == ([16]byte)(zeroUUID) {
		t.Error("expected non-zero UUID")
	}
}
