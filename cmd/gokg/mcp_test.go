package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hungpdn/gokg/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPHTTPURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:8080/mcp", mcpHTTPURL("", ""))
	assert.Equal(t, "http://127.0.0.1:9090/mcp", mcpHTTPURL(":9090", "mcp"))
	assert.Equal(t, "http://0.0.0.0:8080/api/mcp", mcpHTTPURL("0.0.0.0:8080", "/api/mcp"))
}

func TestOpenWatchStorageWithRetryEventuallyOpens(t *testing.T) {
	wantErr := errors.New("Cannot acquire directory lock: resource temporarily unavailable")
	store := noopStorage{}
	attempts := 0

	got, err := openWatchStorageWithRetry(
		context.Background(),
		".gokg/",
		func(path string) (storage.Storage, error) {
			attempts++
			if attempts < 3 {
				return nil, wantErr
			}
			return store, nil
		},
		time.Second,
		time.Millisecond,
	)

	require.NoError(t, err)
	assert.Equal(t, store, got)
	assert.Equal(t, 3, attempts)
}

func TestOpenWatchStorageWithRetryStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := openWatchStorageWithRetry(
		ctx,
		".gokg/",
		func(path string) (storage.Storage, error) {
			return nil, errors.New("Cannot acquire directory lock: resource temporarily unavailable")
		},
		time.Second,
		time.Millisecond,
	)

	assert.ErrorIs(t, err, context.Canceled)
}

type noopStorage struct{}

func (noopStorage) Put(ctx context.Context, key []byte, value []byte) error {
	return nil
}

func (noopStorage) Get(ctx context.Context, key []byte) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (noopStorage) Iterate(ctx context.Context, fn func(key []byte, value []byte) error) error {
	return nil
}

func (noopStorage) Delete(ctx context.Context, key []byte) error {
	return nil
}

func (noopStorage) Close() error {
	return nil
}
