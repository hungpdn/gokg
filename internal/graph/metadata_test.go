package graph

import (
	"context"
	"testing"
	"time"

	"github.com/hungpdn/gokg/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalysisMetadataSaveLoad(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer closeTestStore(t, store)

	_, ok, err := LoadAnalysisMetadata(ctx, store)
	require.NoError(t, err)
	assert.False(t, ok)

	meta := AnalysisMetadata{
		SchemaVersion:     AnalysisMetadataSchemaVersion,
		GoKGVersion:       "test",
		AnalyzedAt:        time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
		RepoID:            "repo-a",
		RepoRoot:          "/repo",
		ModulePrefix:      "example.com/repo",
		IncludeTests:      true,
		GitAvailable:      true,
		GitRoot:           "/repo",
		GitHead:           "abc123",
		GitDirtyAtAnalyze: true,
	}
	require.NoError(t, SaveAnalysisMetadata(ctx, store, meta))

	got, ok, err := LoadAnalysisMetadata(ctx, store)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, meta.RepoID, got.RepoID)
	assert.Equal(t, meta.GitHead, got.GitHead)
	assert.True(t, got.IncludeTests)
	assert.True(t, got.GitDirtyAtAnalyze)
}
