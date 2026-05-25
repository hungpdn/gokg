package storage

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBadgerStorage(t *testing.T) {
	// Create a temporary directory for the test database
	dir, err := os.MkdirTemp("", "badger-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(dir) // clean up

	// Initialize the storage
	store, err := NewBadgerStorage(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	key := []byte("test-key")
	value := []byte("test-value")

	// Test Get on missing key
	_, err = store.Get(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")

	// Test Put
	err = store.Put(ctx, key, value)
	assert.NoError(t, err)

	// Test Get on existing key
	val, err := store.Get(ctx, key)
	assert.NoError(t, err)
	assert.Equal(t, value, val)
}

func TestBadgerStorageContextCancel(t *testing.T) {
	dir, err := os.MkdirTemp("", "badger-test-cancel-*")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	store, err := NewBadgerStorage(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err = store.Put(ctx, []byte("key"), []byte("val"))
	assert.ErrorIs(t, err, context.Canceled)

	_, err = store.Get(ctx, []byte("key"))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestBadgerStorageIterate(t *testing.T) {
	dir, err := os.MkdirTemp("", "badger-test-iterate-*")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	store, err := NewBadgerStorage(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	_ = store.Put(ctx, []byte("node:1"), []byte("data1"))
	_ = store.Put(ctx, []byte("node:2"), []byte("data2"))
	_ = store.Put(ctx, []byte("edge:1"), []byte("data3"))

	count := 0
	err = store.Iterate(ctx, func(key []byte, value []byte) error {
		count++
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}
