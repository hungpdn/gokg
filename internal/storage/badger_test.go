package storage

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBadgerStorage(t *testing.T) {
	dir := t.TempDir()

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

	// Test Delete
	err = store.Delete(ctx, key)
	assert.NoError(t, err)

	_, err = store.Get(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

func TestBadgerStorageContextCancel(t *testing.T) {
	dir := t.TempDir()

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
	dir := t.TempDir()

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

func TestBadgerStoragePutBatch(t *testing.T) {
	dir := t.TempDir()

	store, err := NewBadgerStorage(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	batchStore := store.(BatchPutter)
	require.NoError(t, batchStore.PutBatch(ctx, []Entry{
		{Key: []byte("node:1"), Value: []byte("data1")},
		{Key: []byte("edge:1"), Value: []byte("data2")},
	}))

	val, err := store.Get(ctx, []byte("node:1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("data1"), val)
	val, err = store.Get(ctx, []byte("edge:1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("data2"), val)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	err = batchStore.PutBatch(canceled, []Entry{{Key: []byte("node:2"), Value: []byte("data3")}})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestBadgerStorageIteratePrefix(t *testing.T) {
	dir := t.TempDir()

	store, err := NewBadgerStorage(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	require.NoError(t, store.Put(ctx, []byte("node:1"), []byte("data1")))
	require.NoError(t, store.Put(ctx, []byte("node:2"), []byte("data2")))
	require.NoError(t, store.Put(ctx, []byte("edge:1"), []byte("data3")))

	var keys []string
	prefixStore := store.(PrefixIterator)
	err = prefixStore.IteratePrefix(ctx, []byte("node:"), func(key []byte, value []byte) error {
		keys = append(keys, string(key))
		return nil
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"node:1", "node:2"}, keys)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	err = prefixStore.IteratePrefix(canceled, []byte("node:"), func(key []byte, value []byte) error {
		return nil
	})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestBadgerStorageReadOnly(t *testing.T) {
	dir := t.TempDir()

	store, err := NewBadgerStorage(dir)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Put(ctx, []byte("node:1"), []byte("data1")))
	require.NoError(t, store.Close())

	readOnlyStore, err := NewBadgerStorageReadOnly(dir)
	require.NoError(t, err)
	defer readOnlyStore.Close()

	val, err := readOnlyStore.Get(ctx, []byte("node:1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("data1"), val)

	err = readOnlyStore.Put(ctx, []byte("node:2"), []byte("data2"))
	if runtime.GOOS == "windows" {
		assert.NoError(t, err)
		return
	}
	assert.Error(t, err)
}

func TestBadgerStorageCompactOptions(t *testing.T) {
	dir := t.TempDir()

	opts := badgerOptions(dir)

	assert.Equal(t, dir, opts.Dir)
	assert.Equal(t, dir, opts.ValueDir)
	assert.Nil(t, opts.Logger)
	assert.Equal(t, badgerMemTableSize, opts.MemTableSize)
	assert.Equal(t, badgerValueLogFileSize, opts.ValueLogFileSize)
	assert.Equal(t, badgerValueThreshold, opts.ValueThreshold)
}

func TestBadgerStorageValueLogGCNoRewrite(t *testing.T) {
	dir := t.TempDir()

	store, err := NewBadgerStorage(dir)
	require.NoError(t, err)
	defer store.Close()

	gcStore, ok := store.(ValueLogGCer)
	require.True(t, ok)

	err = gcStore.RunValueLogGC(context.Background(), 0.5)
	require.NoError(t, err)
}
