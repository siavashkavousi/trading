package order

import "github.com/google/uuid"

func NewOrderID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}
