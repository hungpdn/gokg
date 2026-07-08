package implicit

import "fmt"

// PaymentProcessor is our target interface.
type PaymentProcessor interface {
	Process(amount float64) bool
}

// StripeProcessor is one concrete payment processor.
type StripeProcessor struct {
	APIKey string
}

// Process sends the payment to Stripe.
func (s *StripeProcessor) Process(amount float64) bool {
	fmt.Printf("Processing %f via Stripe\n", amount)
	return true
}
