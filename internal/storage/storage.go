package storage

import "context"

// Storage defines the interface for local database operations.
type Storage interface {
	// Put stores a key-value pair in the database.
	Put(ctx context.Context, key []byte, value []byte) error

	// Get retrieves a value by key from the database.
	Get(ctx context.Context, key []byte) ([]byte, error)

	// Iterate iterates over all key-value pairs in the database. The key and
	// value slices passed to fn are only valid until fn returns; copy them before
	// retaining them.
	Iterate(ctx context.Context, fn func(key []byte, value []byte) error) error

	// Delete removes a key-value pair from the database.
	Delete(ctx context.Context, key []byte) error

	// Close cleanly shuts down the database.
	Close() error
}

// Entry is a key-value write used by batch-capable storage backends.
type Entry struct {
	Key   []byte
	Value []byte
}

// BatchPutter is implemented by storage backends that can persist multiple
// entries with less per-key overhead than repeated Put calls.
type BatchPutter interface {
	PutBatch(ctx context.Context, entries []Entry) error
}

// PrefixIterator is implemented by storage backends that can efficiently scan
// keys with a shared prefix. The key and value slices passed to fn are only
// valid until fn returns; copy them before retaining them.
type PrefixIterator interface {
	IteratePrefix(ctx context.Context, prefix []byte, fn func(key []byte, value []byte) error) error
}

// ValueLogGCer is implemented by storage backends that can compact stale value
// log entries after heavy write/delete cycles.
type ValueLogGCer interface {
	RunValueLogGC(ctx context.Context, discardRatio float64) error
}
