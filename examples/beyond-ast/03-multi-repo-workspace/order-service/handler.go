package main

import (
	"fmt"
	"net/http"

	event "example.com/acme/shared-libs"
)

// RegisterRoutes exposes the order API.
func RegisterRoutes() {
	http.HandleFunc("/orders", CreateOrderHandler)
}

// CreateOrderHandler is the HTTP entry point for order creation.
func CreateOrderHandler(w http.ResponseWriter, r *http.Request) {
	data, err := ProcessOrder("ORD-123", 99.99)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, _ = w.Write(data)
}

// ProcessOrder creates a new order and dispatches a shared event.
// This service lives in a different repository than shared-libs.
func ProcessOrder(id string, amount float64) ([]byte, error) {
	fmt.Printf("Processing order %s\n", id)

	ev := event.OrderCreated{
		OrderID: id,
		Amount:  amount,
	}

	if err := ev.Validate(); err != nil {
		return nil, err
	}

	fmt.Printf("Publishing to topic %s\n", ev.Topic())
	return ev.Marshal()
}

func main() {
	RegisterRoutes()
	_, _ = ProcessOrder("ORD-123", 99.99)
}
