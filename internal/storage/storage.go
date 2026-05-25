package storage

import "context"

// Storage defines the interface for local database operations.
type Storage interface {
	// Put stores a key-value pair in the database.
	Put(ctx context.Context, key []byte, value []byte) error

	// Get retrieves a value by key from the database.
	Get(ctx context.Context, key []byte) ([]byte, error)

	// Close cleanly shuts down the database.
	Close() error
}
