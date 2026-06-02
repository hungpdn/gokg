package storage

import "context"

// Storage defines the interface for local database operations.
type Storage interface {
	// Put stores a key-value pair in the database.
	Put(ctx context.Context, key []byte, value []byte) error

	// Get retrieves a value by key from the database.
	Get(ctx context.Context, key []byte) ([]byte, error)

	// Iterate iterates over all key-value pairs in the database.
	Iterate(ctx context.Context, fn func(key []byte, value []byte) error) error

	// Delete removes a key-value pair from the database.
	Delete(ctx context.Context, key []byte) error

	// Close cleanly shuts down the database.
	Close() error
}

// ValueLogGCer is implemented by storage backends that can compact stale value
// log entries after heavy write/delete cycles.
type ValueLogGCer interface {
	RunValueLogGC(ctx context.Context, discardRatio float64) error
}
