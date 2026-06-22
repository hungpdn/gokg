package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	workspaceDirName = ".gokg/workspaces"
	configFileName   = "workspace.json"
)

// Config represents the workspace configuration.
type Config struct {
	Name  string            `json:"name"`
	Repos map[string]string `json:"repos"` // map[repoID]absolutePath
}

// Workspace manages a multi-repo environment.
type Workspace struct {
	Name   string
	Dir    string
	Config Config
}

// GetHomeDir returns the base directory for all workspaces.
func GetHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home dir: %w", err)
	}
	return filepath.Join(home, workspaceDirName), nil
}

// Init creates a new workspace.
func Init(name string) (*Workspace, error) {
	cleanName, err := validateName(name)
	if err != nil {
		return nil, err
	}
	name = cleanName

	home, err := GetHomeDir()
	if err != nil {
		return nil, err
	}

	wsDir := filepath.Join(home, name)
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workspace dir: %w", err)
	}

	configPath := filepath.Join(wsDir, configFileName)
	if _, err := os.Stat(configPath); err == nil {
		return nil, fmt.Errorf("workspace %q already exists", name)
	}

	ws := &Workspace{
		Name: name,
		Dir:  wsDir,
		Config: Config{
			Name:  name,
			Repos: make(map[string]string),
		},
	}

	if err := ws.Save(); err != nil {
		return nil, err
	}

	return ws, nil
}

// Load loads an existing workspace.
func Load(name string) (*Workspace, error) {
	cleanName, err := validateName(name)
	if err != nil {
		return nil, err
	}
	name = cleanName

	home, err := GetHomeDir()
	if err != nil {
		return nil, err
	}

	wsDir := filepath.Join(home, name)
	configPath := filepath.Join(wsDir, configFileName)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace %q: %w", name, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse workspace config: %w", err)
	}

	if cfg.Repos == nil {
		cfg.Repos = make(map[string]string)
	}
	if cfg.Name == "" {
		cfg.Name = name
	}

	ws := &Workspace{
		Name:   name,
		Dir:    wsDir,
		Config: cfg,
	}
	if err := ws.validateConfig(); err != nil {
		return nil, err
	}

	return ws, nil
}

// Save writes the workspace configuration to disk.
func (ws *Workspace) Save() error {
	configPath := filepath.Join(ws.Dir, configFileName)
	data, err := json.MarshalIndent(ws.Config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode workspace config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workspace config: %w", err)
	}
	return nil
}

// AddRepo adds a repository to the workspace.
func (ws *Workspace) AddRepo(repoID string, absPath string) error {
	repoID = strings.TrimSpace(repoID)
	if err := validateRepoID(repoID); err != nil {
		return err
	}
	absPath = strings.TrimSpace(absPath)
	if err := validateRepoPath(absPath); err != nil {
		return err
	}

	if ws.Config.Repos == nil {
		ws.Config.Repos = make(map[string]string)
	}
	if _, exists := ws.Config.Repos[repoID]; exists {
		return fmt.Errorf("repo %q already exists in workspace %q", repoID, ws.Name)
	}

	dbPath := filepath.Clean(ws.GetRepoDBPath(repoID))
	for existingRepoID := range ws.Config.Repos {
		if filepath.Clean(ws.GetRepoDBPath(existingRepoID)) == dbPath {
			return fmt.Errorf("repo %q database path collides with existing repo %q at %s", repoID, existingRepoID, dbPath)
		}
	}

	ws.Config.Repos[repoID] = absPath
	return ws.Save()
}

// GetRepoDBPath returns the BadgerDB path for a specific repository.
func (ws *Workspace) GetRepoDBPath(repoID string) string {
	// e.g. ~/.gokg/workspaces/my-workspace/github.com_org_repo.db
	safeID := filepath.ToSlash(repoID)
	safeID = fmt.Sprintf("%s.db", strings.ReplaceAll(safeID, "/", "_"))
	return filepath.Join(ws.Dir, safeID)
}

// RemoveRepo removes a repository from the workspace config and deletes its DB.
func (ws *Workspace) RemoveRepo(repoID string) error {
	if _, ok := ws.Config.Repos[repoID]; !ok {
		return fmt.Errorf("repo %q not found in workspace %q", repoID, ws.Name)
	}

	// Remove the BadgerDB directory for this repo
	dbPath := ws.GetRepoDBPath(repoID)
	if err := os.RemoveAll(dbPath); err != nil {
		return fmt.Errorf("failed to remove database for repo %q: %w", repoID, err)
	}

	delete(ws.Config.Repos, repoID)
	return ws.Save()
}

// List returns the names of all workspaces.
func List() ([]string, error) {
	home, err := GetHomeDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			configPath := filepath.Join(home, entry.Name(), configFileName)
			if _, err := os.Stat(configPath); err == nil {
				names = append(names, entry.Name())
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("workspace name cannot be empty")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid workspace name %q", name)
	}
	return name, nil
}

func (ws *Workspace) validateConfig() error {
	if ws.Config.Name != ws.Name {
		return fmt.Errorf("workspace config name %q does not match workspace %q", ws.Config.Name, ws.Name)
	}

	dbPaths := make(map[string]string, len(ws.Config.Repos))
	for repoID, repoPath := range ws.Config.Repos {
		if err := validateRepoID(repoID); err != nil {
			return err
		}
		if err := validateRepoPath(repoPath); err != nil {
			return fmt.Errorf("repo %q: %w", repoID, err)
		}

		dbPath := filepath.Clean(ws.GetRepoDBPath(repoID))
		if existingRepoID, exists := dbPaths[dbPath]; exists {
			return fmt.Errorf("repo %q database path collides with existing repo %q at %s", repoID, existingRepoID, dbPath)
		}
		dbPaths[dbPath] = repoID
	}

	return nil
}

func validateRepoID(repoID string) error {
	if strings.TrimSpace(repoID) != repoID {
		return fmt.Errorf("repo ID %q has leading or trailing whitespace", repoID)
	}
	if repoID == "" {
		return fmt.Errorf("repo ID cannot be empty")
	}
	return nil
}

func validateRepoPath(repoPath string) error {
	if strings.TrimSpace(repoPath) != repoPath {
		return fmt.Errorf("repo path %q has leading or trailing whitespace", repoPath)
	}
	if repoPath == "" {
		return fmt.Errorf("repo path cannot be empty")
	}
	return nil
}
