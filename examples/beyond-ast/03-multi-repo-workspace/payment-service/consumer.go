package main

import (
	"fmt"

	event "example.com/acme/shared-libs"
)

// ConsumeOrderCreated handles events published by order-service.
// It depends on the same shared event contract from shared-libs.
func ConsumeOrderCreated(ev event.OrderCreated) error {
	if err := ev.Validate(); err != nil {
		return err
	}

	fmt.Printf("Consuming topic %s for order %s\n", ev.Topic(), ev.OrderID)
	return nil
}

func main() {
	_ = ConsumeOrderCreated(event.OrderCreated{
		OrderID: "ORD-123",
		Amount:  99.99,
	})
}
