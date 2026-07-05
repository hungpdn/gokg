package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hungpdn/gokg/internal/storage"
)

const (
	AnalysisMetadataSchemaVersion = 1
	analysisMetadataKey           = "meta:analysis:v1"
)

// AnalysisMetadata describes the repository state used to build a graph
// snapshot. It is stored once per Badger database.
type AnalysisMetadata struct {
	SchemaVersion        int       `json:"schema_version"`
	GoKGVersion          string    `json:"gokg_version,omitempty"`
	AnalyzedAt           time.Time `json:"analyzed_at"`
	RepoID               string    `json:"repo_id,omitempty"`
	RepoRoot             string    `json:"repo_root,omitempty"`
	ModulePrefix         string    `json:"module_prefix,omitempty"`
	WorkspaceName        string    `json:"workspace_name,omitempty"`
	IncludeTests         bool      `json:"include_tests"`
	GitAvailable         bool      `json:"git_available"`
	GitError             string    `json:"git_error,omitempty"`
	GitRoot              string    `json:"git_root,omitempty"`
	GitHead              string    `json:"git_head,omitempty"`
	GitBranch            string    `json:"git_branch,omitempty"`
	GitDirtyAtAnalyze    bool      `json:"git_dirty_at_analyze"`
	GitStatusFingerprint string    `json:"git_status_fingerprint,omitempty"`
}

func SaveAnalysisMetadata(ctx context.Context, store storage.Storage, meta AnalysisMetadata) error {
	if store == nil {
		return fmt.Errorf("storage backend is required")
	}
	if meta.SchemaVersion == 0 {
		meta.SchemaVersion = AnalysisMetadataSchemaVersion
	}
	if meta.AnalyzedAt.IsZero() {
		meta.AnalyzedAt = time.Now().UTC()
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal analysis metadata: %w", err)
	}
	if err := store.Put(ctx, []byte(analysisMetadataKey), data); err != nil {
		return fmt.Errorf("persist analysis metadata: %w", err)
	}
	return nil
}

func LoadAnalysisMetadata(ctx context.Context, store storage.Storage) (AnalysisMetadata, bool, error) {
	if store == nil {
		return AnalysisMetadata{}, false, fmt.Errorf("storage backend is required")
	}
	data, err := store.Get(ctx, []byte(analysisMetadataKey))
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return AnalysisMetadata{}, false, nil
		}
		return AnalysisMetadata{}, false, fmt.Errorf("load analysis metadata: %w", err)
	}
	var meta AnalysisMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return AnalysisMetadata{}, false, fmt.Errorf("decode analysis metadata: %w", err)
	}
	return meta, true, nil
}
