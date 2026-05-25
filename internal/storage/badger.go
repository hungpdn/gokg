package storage

import (
	"context"
	"fmt"

	"github.com/dgraph-io/badger/v4"
)

type badgerStorage struct {
	db *badger.DB
}

// NewBadgerStorage initializes a new BadgerDB instance at the given path.
func NewBadgerStorage(path string) (Storage, error) {
	opts := badger.DefaultOptions(path).WithLogger(nil) // Disable default logger for cleaner CLI output
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db at %s: %w", path, err)
	}

	return &badgerStorage{db: db}, nil
}

func (b *badgerStorage) Put(ctx context.Context, key []byte, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := b.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, value)
	})
	if err != nil {
		return fmt.Errorf("failed to put key: %w", err)
	}
	return nil
}

func (b *badgerStorage) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var valCopy []byte
	err := b.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		valCopy, err = item.ValueCopy(nil)
		return err
	})
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return nil, fmt.Errorf("key not found")
		}
		return nil, fmt.Errorf("failed to get key: %w", err)
	}
	return valCopy, nil
}

func (b *badgerStorage) Iterate(ctx context.Context, fn func(key []byte, value []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			item := it.Item()
			k := item.Key()
			err := item.Value(func(v []byte) error {
				return fn(k, v)
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (b *badgerStorage) Close() error {
	if err := b.db.Close(); err != nil {
		return fmt.Errorf("failed to close badger db: %w", err)
	}
	return nil
}
