package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/dgraph-io/badger/v4"
)

const (
	badgerMemTableSize     int64 = 8 << 20
	badgerValueLogFileSize int64 = 16 << 20
	badgerValueThreshold   int64 = 1 << 20
)

type badgerStorage struct {
	db *badger.DB
}

// NewBadgerStorage initializes a new BadgerDB instance at the given path.
func NewBadgerStorage(path string) (Storage, error) {
	opts := badgerOptions(path)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db at %s: %w", path, err)
	}

	return &badgerStorage{db: db}, nil
}

// NewBadgerStorageReadOnly opens an existing BadgerDB instance without taking
// the writer role. It is intended for graph hydration/export paths.
func NewBadgerStorageReadOnly(path string) (Storage, error) {
	opts := badgerOptions(path).WithReadOnly(true)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db at %s in read-only mode: %w", path, err)
	}

	return &badgerStorage{db: db}, nil
}

func badgerOptions(path string) badger.Options {
	opts := badger.DefaultOptions(path).
		WithLogger(nil). // Disable default logger for cleaner CLI output
		WithMemTableSize(badgerMemTableSize).
		WithValueLogFileSize(badgerValueLogFileSize).
		WithValueThreshold(badgerValueThreshold).
		WithBlockCacheSize(16 << 20). // 16 MB block cache (default is 256MB)
		WithIndexCacheSize(16 << 20). // 16 MB index cache (default is 0, unbounded)
		WithBypassLockGuard(false)
	return opts
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

func (b *badgerStorage) IteratePrefix(ctx context.Context, prefix []byte, fn func(key []byte, value []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
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

func (b *badgerStorage) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := b.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
	if err != nil && err != badger.ErrKeyNotFound {
		return fmt.Errorf("failed to delete key: %w", err)
	}
	return nil
}

func (b *badgerStorage) RunValueLogGC(ctx context.Context, discardRatio float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		err := b.db.RunValueLogGC(discardRatio)
		if err == nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			continue
		}
		if errors.Is(err, badger.ErrNoRewrite) {
			return nil
		}
		return fmt.Errorf("failed to run badger value log GC: %w", err)
	}
}

func (b *badgerStorage) Close() error {
	if err := b.db.Close(); err != nil {
		return fmt.Errorf("failed to close badger db: %w", err)
	}
	return nil
}
