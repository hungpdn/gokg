package event

import "fmt"

// OrderCreated is an event emitted by multiple services.
// If this struct changes, we need to know all cross-repo impacts.
type OrderCreated struct {
	OrderID string
	Amount  float64
}

// Topic returns the message topic used by services that publish or consume this event.
func (e *OrderCreated) Topic() string {
	return "orders.created"
}

// Validate is shared by every service that consumes the event contract.
func (e *OrderCreated) Validate() error {
	if e.OrderID == "" {
		return fmt.Errorf("order id is required")
	}
	if e.Amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	return nil
}

// Marshal serializes the event for a message queue.
func (e *OrderCreated) Marshal() ([]byte, error) {
	return []byte(`{"id": "` + e.OrderID + `"}`), nil
}
