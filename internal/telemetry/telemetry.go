package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultFile                        = ".gokg/telemetry.jsonl"
	DefaultAsyncQueueSize              = 1024
	DefaultAsyncCloseTimeout           = 5 * time.Second
	DefaultMaxFileBytes          int64 = 64 << 20
	DefaultMaxBackups                  = 4
	MaxLabelRunes                      = 256
	MaxJSONLLineBytes                  = 1 << 20
	eventVersion                       = 2
	legacyEventVersion                 = 1
	tokenByteSize                      = 4
	maxReportGroupLimit                = 4096
	maxTelemetrySegments               = 64
	maxTelemetrySnapshotAttempts       = 3
	latencyExactBuckets                = 16
	latencySubBuckets                  = 16
	latencyExponentBuckets             = 59
	latencyBucketCount                 = latencyExactBuckets + latencySubBuckets*latencyExponentBuckets
)

var (
	ErrRecorderClosed           = errors.New("telemetry recorder closed")
	ErrNilRecorder              = errors.New("telemetry recorder is nil")
	ErrEventTooLarge            = errors.New("telemetry event exceeds maximum file size")
	errUnsupportedEventVersion  = errors.New("unsupported telemetry event version")
	errTelemetrySnapshotChanged = errors.New("telemetry segments changed while creating report snapshot")
	lockDirectoryOverride       atomic.Pointer[string]
)

// Event is one append-only MCP tool-call telemetry record.
type Event struct {
	Version               int       `json:"version"`
	Timestamp             time.Time `json:"timestamp"`
	SessionID             string    `json:"session_id,omitempty"`
	ClientName            string    `json:"client_name,omitempty"`
	ClientVersion         string    `json:"client_version,omitempty"`
	UserAgent             string    `json:"user_agent,omitempty"`
	Transport             string    `json:"transport"`
	ToolName              string    `json:"tool_name"`
	Success               bool      `json:"success"`
	DeliveryError         bool      `json:"delivery_error,omitempty"`
	ErrorCode             int       `json:"error_code,omitempty"`
	ErrorKind             string    `json:"error_kind,omitempty"`
	DurationUS            int64     `json:"duration_us"`
	DurationMS            int64     `json:"duration_ms,omitempty"`
	RequestBytes          int       `json:"request_bytes"`
	ResponseBytes         int       `json:"response_bytes"`
	EstimatedInputTokens  int       `json:"estimated_input_tokens"`
	EstimatedOutputTokens int       `json:"estimated_output_tokens"`
}

// Recorder stores telemetry events. Implementations should be safe for
// concurrent MCP HTTP requests.
type Recorder interface {
	Record(context.Context, Event) error
}

// JSONLRecorder appends one telemetry event per line.
type JSONLRecorder struct {
	mu           sync.Mutex
	root         *os.Root
	lockFile     *os.File
	file         *os.File
	path         string
	name         string
	size         int64
	maxFileBytes int64
	maxBackups   int
}

type JSONLRecorderOptions struct {
	Path         string
	MaxFileBytes int64
	MaxBackups   int
}

func NewJSONLRecorder(path string) (*JSONLRecorder, error) {
	return NewJSONLRecorderWithOptions(JSONLRecorderOptions{
		Path: path, MaxFileBytes: DefaultMaxFileBytes, MaxBackups: DefaultMaxBackups,
	})
}

func NewJSONLRecorderWithOptions(options JSONLRecorderOptions) (*JSONLRecorder, error) {
	path := normalizePath(options.Path)
	if options.MaxFileBytes < 0 {
		return nil, fmt.Errorf("maximum telemetry file size must be non-negative")
	}
	if options.MaxFileBytes == 0 {
		options.MaxFileBytes = DefaultMaxFileBytes
	}
	if options.MaxBackups < 0 {
		return nil, fmt.Errorf("maximum telemetry backups must be non-negative")
	}
	if options.MaxBackups > maxTelemetrySegments {
		return nil, fmt.Errorf("maximum telemetry backups exceeds %d", maxTelemetrySegments)
	}
	dir, name, err := splitTelemetryPath(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create telemetry directory: %w", err)
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect telemetry directory: %w", err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return nil, fmt.Errorf("telemetry directory must be a real directory, not a symlink")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open telemetry root: %w", err)
	}
	openedDirInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(dirInfo, openedDirInfo) {
		_ = root.Close()
		if err != nil {
			return nil, fmt.Errorf("verify opened telemetry root: %w", err)
		}
		return nil, fmt.Errorf("telemetry directory changed while opening")
	}
	lockFile, _, err := acquireStableTelemetryLock(path)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("lock telemetry file %q (use a distinct --telemetry-file per MCP process): %w", path, err)
	}
	closeLockOnError := func() {
		_ = unlockTelemetryFile(lockFile)
		_ = lockFile.Close()
		_ = root.Close()
	}
	file, info, err := openSecureRegularFile(root, name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, true)
	if err != nil {
		closeLockOnError()
		return nil, fmt.Errorf("open telemetry file: %w", err)
	}
	if err := pruneTelemetryBackups(root, name, options.MaxBackups, telemetryFilesystemIsCaseInsensitive(dir)); err != nil {
		closeErr := file.Close()
		closeLockOnError()
		return nil, errors.Join(fmt.Errorf("apply telemetry backup retention: %w", err), closeErr)
	}
	return &JSONLRecorder{
		root: root, lockFile: lockFile, file: file, path: path, name: name, size: info.Size(),
		maxFileBytes: options.MaxFileBytes, maxBackups: options.MaxBackups,
	}, nil
}

func (r *JSONLRecorder) Record(ctx context.Context, event Event) error {
	if r == nil {
		return ErrRecorderClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	event, _, err := normalizeEventForWrite(event)
	if err != nil {
		return err
	}
	return r.recordPrepared(ctx, event)
}

// recordPrepared is an internal fast path for AsyncRecorder. The async queue
// only contains events that normalizeEventForWrite has already validated.
func (r *JSONLRecorder) recordPrepared(ctx context.Context, event Event) error {
	if r == nil {
		return ErrRecorderClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal telemetry event: %w", err)
	}
	line = append(line, '\n')
	lineSize := int64(len(line))
	if lineSize > r.maxFileBytes {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrEventTooLarge, lineSize, r.maxFileBytes)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return ErrRecorderClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.size > r.maxFileBytes-lineSize {
		if err := r.rotateLocked(); err != nil {
			return err
		}
	}
	n, err := r.file.Write(line)
	r.size += int64(n)
	if err != nil {
		return fmt.Errorf("write telemetry event: %w", err)
	}
	if n != len(line) {
		return fmt.Errorf("write telemetry event: %w", io.ErrShortWrite)
	}
	return nil
}

func (r *JSONLRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil && r.lockFile == nil && r.root == nil {
		return nil
	}
	var closeErr error
	if r.file != nil {
		closeErr = errors.Join(closeErr, r.file.Close())
		r.file = nil
	}
	if r.lockFile != nil {
		closeErr = errors.Join(closeErr, unlockTelemetryFile(r.lockFile), r.lockFile.Close())
		r.lockFile = nil
	}
	if r.root != nil {
		closeErr = errors.Join(closeErr, r.root.Close())
		r.root = nil
	}
	return closeErr
}

func openTelemetryLockFile(root *os.Root, name string) (*os.File, bool, error) {
	_, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		file, _, createErr := openSecureRegularFile(root, name, os.O_CREATE|os.O_RDWR, true)
		if createErr == nil {
			return file, true, nil
		}
		if !errors.Is(createErr, os.ErrExist) {
			return nil, false, createErr
		}
	} else if err != nil {
		return nil, false, err
	}
	file, _, err := openSecureRegularFile(root, name, os.O_RDWR, true)
	return file, false, err
}

// FileLease is a non-blocking, process-scoped advisory lease for one telemetry
// file series. It is used by the writer and destructive maintenance paths.
type FileLease struct {
	mu   sync.Mutex
	file *os.File
}

// TryAcquireFileLease locks the sidecar for path without opening or creating
// the telemetry data file. created reports whether this call created the
// sidecar, allowing maintenance callers to remove their temporary sidecar.
func TryAcquireFileLease(path string) (*FileLease, bool, error) {
	file, created, err := acquireStableTelemetryLock(path)
	if err != nil {
		return nil, created, err
	}
	return &FileLease{file: file}, created, nil
}

func acquireStableTelemetryLock(path string) (*os.File, bool, error) {
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, false, err
	}
	dataDir := filepath.Dir(absPath)
	dataDirInfo, err := os.Lstat(dataDir)
	if err != nil {
		return nil, false, err
	}
	if dataDirInfo.Mode()&os.ModeSymlink != 0 || !dataDirInfo.IsDir() {
		return nil, false, fmt.Errorf("telemetry directory must be a real directory, not a symlink")
	}
	resolvedDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return nil, false, err
	}
	key := filepath.Join(resolvedDataDir, filepath.Base(absPath))
	if telemetryFilesystemIsCaseInsensitive(dataDir) {
		key = strings.ToLower(key)
	}
	hash := sha256.Sum256([]byte(key))
	lockName := fmt.Sprintf("telemetry-%x.lock", hash[:16])

	lockDir, err := telemetryLockDirectory()
	if err != nil {
		return nil, false, err
	}
	lockDirInfo, err := os.Lstat(lockDir)
	if err != nil {
		return nil, false, err
	}
	if lockDirInfo.Mode()&os.ModeSymlink != 0 || !lockDirInfo.IsDir() {
		return nil, false, fmt.Errorf("telemetry lock directory must be a real directory, not a symlink")
	}

	root, err := os.OpenRoot(lockDir)
	if err != nil {
		return nil, false, err
	}
	openedDirInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(lockDirInfo, openedDirInfo) {
		_ = root.Close()
		if err != nil {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("telemetry lock directory changed while opening")
	}
	file, created, err := openTelemetryLockFile(root, lockName)
	if err != nil {
		_ = root.Close()
		return nil, created, err
	}
	if err := lockTelemetryFile(file); err != nil {
		_ = file.Close()
		_ = root.Close()
		return nil, created, err
	}
	if err := root.Close(); err != nil {
		_ = unlockTelemetryFile(file)
		_ = file.Close()
		return nil, created, err
	}
	return file, created, nil
}

func telemetryLockDirectory() (string, error) {
	var dir string
	if override := lockDirectoryOverride.Load(); override != nil {
		dir = *override
	} else {
		var err error
		dir, err = defaultTelemetryLockDirectory()
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create telemetry lock directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("inspect telemetry lock directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("telemetry lock directory must be a real directory, not a symlink")
	}
	if err := validateTelemetryLockDirectoryOwner(info); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("secure telemetry lock directory: %w", err)
	}
	after, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("reinspect telemetry lock directory: %w", err)
	}
	if !after.IsDir() || !os.SameFile(info, after) {
		return "", fmt.Errorf("telemetry lock directory changed while securing it")
	}
	if err := validateTelemetryLockDirectoryOwner(after); err != nil {
		return "", err
	}
	return dir, nil
}

// SetLockDirectoryForTesting overrides the process-wide lease directory.
// Tests must install it before starting concurrent recorder operations.
func SetLockDirectoryForTesting(dir string) func() {
	value := filepath.Clean(strings.TrimSpace(dir))
	if value == "." || value == "" {
		panic("telemetry test lock directory must not be empty")
	}
	previous := lockDirectoryOverride.Swap(&value)
	return func() {
		lockDirectoryOverride.Store(previous)
	}
}

func telemetryFilesystemIsCaseInsensitive(path string) bool {
	current := path
	if absolute, err := filepath.Abs(path); err == nil {
		current = absolute
	}
	for {
		info, err := os.Stat(current)
		if err == nil {
			base := filepath.Base(current)
			for index := 0; index < len(base); index++ {
				character := base[index]
				var replacement byte
				switch {
				case character >= 'a' && character <= 'z':
					replacement = character - ('a' - 'A')
				case character >= 'A' && character <= 'Z':
					replacement = character + ('a' - 'A')
				default:
					continue
				}
				alternate := base[:index] + string(replacement) + base[index+1:]
				alternateInfo, alternateErr := os.Stat(filepath.Join(filepath.Dir(current), alternate))
				return alternateErr == nil && os.SameFile(info, alternateInfo)
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

func (l *FileLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := errors.Join(unlockTelemetryFile(l.file), l.file.Close())
	l.file = nil
	return err
}

func splitTelemetryPath(path string) (string, string, error) {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "", "", fmt.Errorf("invalid telemetry file path %q", path)
	}
	return dir, name, nil
}

func openSecureRegularFile(root *os.Root, name string, flags int, writable bool) (*os.File, os.FileInfo, error) {
	if root == nil {
		return nil, nil, fmt.Errorf("telemetry root is closed")
	}
	before, err := root.Lstat(name)
	createExclusive := false
	if err == nil {
		if err := validateRegularFileInfo(before); err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	} else if flags&os.O_CREATE == 0 {
		return nil, nil, err
	} else {
		createExclusive = true
	}

	if createExclusive {
		flags |= os.O_EXCL
	}
	file, err := root.OpenFile(name, flags, 0o600)
	if err != nil {
		return nil, nil, err
	}
	closeOnError := func(openErr error) (*os.File, os.FileInfo, error) {
		return nil, nil, errors.Join(openErr, file.Close())
	}
	fstat, err := file.Stat()
	if err != nil {
		return closeOnError(err)
	}
	if err := validateRegularFileInfo(fstat); err != nil {
		return closeOnError(err)
	}
	after, err := root.Lstat(name)
	if err != nil {
		return closeOnError(err)
	}
	if err := validateRegularFileInfo(after); err != nil {
		return closeOnError(err)
	}
	if !os.SameFile(after, fstat) {
		return closeOnError(fmt.Errorf("telemetry path changed while opening"))
	}
	if writable {
		if err := file.Chmod(0o600); err != nil {
			return closeOnError(fmt.Errorf("secure telemetry file permissions: %w", err))
		}
		fstat, err = file.Stat()
		if err != nil {
			return closeOnError(err)
		}
		if err := validateRegularFileInfo(fstat); err != nil {
			return closeOnError(err)
		}
	}
	return file, fstat, nil
}

func validateRegularFileInfo(info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("telemetry path is not a regular file")
	}
	return nil
}

func (r *JSONLRecorder) rotateLocked() error {
	if err := r.verifyCurrentFileLocked(); err != nil {
		return fmt.Errorf("verify telemetry file before rotation: %w", err)
	}
	if err := r.file.Close(); err != nil {
		r.file = nil
		return errors.Join(fmt.Errorf("close telemetry file for rotation: %w", err), r.recoverActiveLocked())
	}
	r.file = nil

	if err := r.rotateNamesLocked(); err != nil {
		return errors.Join(fmt.Errorf("rotate telemetry files: %w", err), r.recoverActiveLocked())
	}
	file, info, err := openSecureRegularFile(r.root, r.name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, true)
	if err != nil {
		return errors.Join(fmt.Errorf("create telemetry file after rotation: %w", err), r.recoverActiveLocked())
	}
	r.file = file
	r.size = info.Size()
	return nil
}

func (r *JSONLRecorder) verifyCurrentFileLocked() error {
	if r.root == nil || r.file == nil {
		return ErrRecorderClosed
	}
	fstat, err := r.file.Stat()
	if err != nil {
		return err
	}
	if err := validateRegularFileInfo(fstat); err != nil {
		return err
	}
	lstat, err := r.root.Lstat(r.name)
	if err != nil {
		return err
	}
	if err := validateRegularFileInfo(lstat); err != nil {
		return err
	}
	if !os.SameFile(lstat, fstat) {
		return fmt.Errorf("telemetry path no longer references the open file")
	}
	return nil
}

func (r *JSONLRecorder) rotateNamesLocked() error {
	if r.maxBackups == 0 {
		return removeRegularIfExists(r.root, r.name)
	}
	if err := removeRegularIfExists(r.root, backupName(r.name, r.maxBackups)); err != nil {
		return err
	}
	for index := r.maxBackups - 1; index >= 1; index-- {
		if err := renameRegularIfExists(r.root, backupName(r.name, index), backupName(r.name, index+1)); err != nil {
			return err
		}
	}
	return renameRegularRequired(r.root, r.name, backupName(r.name, 1))
}

func (r *JSONLRecorder) recoverActiveLocked() error {
	if r.root == nil || r.file != nil {
		return nil
	}
	file, info, err := openSecureRegularFile(r.root, r.name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, true)
	if err != nil {
		return fmt.Errorf("recover telemetry file: %w", err)
	}
	r.file = file
	r.size = info.Size()
	return nil
}

func backupName(name string, index int) string {
	return name + "." + strconv.Itoa(index)
}

func pruneTelemetryBackups(root *os.Root, activeName string, maxBackups int, caseInsensitive bool) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	candidates := make([]string, 0, maxTelemetrySegments)
	for {
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			index, isBackup := telemetryBackupIndex(entry.Name(), activeName, caseInsensitive)
			if !isBackup || index <= maxBackups {
				continue
			}
			if len(candidates) >= maxTelemetrySegments {
				_ = directory.Close()
				return fmt.Errorf("too many telemetry backups beyond retention: maximum is %d", maxTelemetrySegments)
			}
			info, statErr := root.Lstat(entry.Name())
			if statErr != nil {
				_ = directory.Close()
				return statErr
			}
			if err := validateRegularFileInfo(info); err != nil {
				_ = directory.Close()
				return fmt.Errorf("refuse to prune %q: %w", entry.Name(), err)
			}
			candidates = append(candidates, entry.Name())
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = directory.Close()
			return readErr
		}
	}
	if err := directory.Close(); err != nil {
		return err
	}
	sort.Strings(candidates)
	var pruneErr error
	for _, name := range candidates {
		pruneErr = errors.Join(pruneErr, removeRegularIfExists(root, name))
	}
	return pruneErr
}

func removeRegularIfExists(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validateRegularFileInfo(info); err != nil {
		return fmt.Errorf("refuse to remove %q: %w", name, err)
	}
	return root.Remove(name)
}

func renameRegularIfExists(root *os.Root, oldName, newName string) error {
	info, err := root.Lstat(oldName)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validateRegularFileInfo(info); err != nil {
		return fmt.Errorf("refuse to rotate %q: %w", oldName, err)
	}
	if _, err := root.Lstat(newName); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("rotation destination %q already exists", newName)
		}
		return err
	}
	return root.Rename(oldName, newName)
}

func renameRegularRequired(root *os.Root, oldName, newName string) error {
	info, err := root.Lstat(oldName)
	if err != nil {
		return err
	}
	if err := validateRegularFileInfo(info); err != nil {
		return fmt.Errorf("refuse to rotate %q: %w", oldName, err)
	}
	if _, err := root.Lstat(newName); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("rotation destination %q already exists", newName)
		}
		return err
	}
	return root.Rename(oldName, newName)
}

type AsyncRecorder struct {
	recorder Recorder
	queue    chan Event
	stop     chan struct{}
	done     chan struct{}

	stateMu      sync.Mutex
	stopping     bool
	producers    sync.WaitGroup
	shutdownOnce sync.Once

	errMu         sync.RWMutex
	firstWriteErr error
	closeErr      error

	accepted atomic.Uint64
	written  atomic.Uint64
	failed   atomic.Uint64
	rejected atomic.Uint64
}

// AsyncStats is an eventually consistent snapshot of asynchronous delivery.
// Once Shutdown completes, Accepted is exactly Written+Failed.
type AsyncStats struct {
	Accepted uint64 `json:"accepted"`
	Written  uint64 `json:"written"`
	Failed   uint64 `json:"failed"`
	Rejected uint64 `json:"rejected"`
}

func (s AsyncStats) Pending() uint64 {
	accounted := s.Written + s.Failed
	if accounted >= s.Accepted {
		return 0
	}
	return s.Accepted - accounted
}

func NewAsyncRecorder(recorder Recorder, capacity int) *AsyncRecorder {
	if capacity <= 0 {
		capacity = DefaultAsyncQueueSize
	}
	r := &AsyncRecorder{
		recorder: recorder,
		queue:    make(chan Event, capacity),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if recorder == nil {
		r.setFirstWriteError(ErrNilRecorder)
	}
	go r.run()
	return r
}

func (r *AsyncRecorder) Record(ctx context.Context, event Event) error {
	if r == nil {
		return ErrRecorderClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		r.rejected.Add(1)
		return err
	}
	if err := r.terminalError(); err != nil {
		r.rejected.Add(1)
		return fmt.Errorf("telemetry recorder failed: %w", err)
	}

	r.stateMu.Lock()
	if r.stopping {
		r.stateMu.Unlock()
		r.rejected.Add(1)
		if err := r.terminalError(); err != nil {
			return fmt.Errorf("telemetry recorder failed: %w", err)
		}
		return ErrRecorderClosed
	}
	r.producers.Add(1)
	r.stateMu.Unlock()
	defer r.producers.Done()

	prepared, _, err := normalizeEventForWrite(event)
	if err != nil {
		r.rejected.Add(1)
		return err
	}

	select {
	case r.queue <- prepared:
		r.accepted.Add(1)
		return nil
	case <-ctx.Done():
		r.rejected.Add(1)
		return ctx.Err()
	case <-r.stop:
		r.rejected.Add(1)
		if err := r.terminalError(); err != nil {
			return fmt.Errorf("telemetry recorder failed: %w", err)
		}
		return ErrRecorderClosed
	}
}

// Shutdown stops accepting events and waits for every accepted event to be
// written or explicitly accounted as failed. A timed-out shutdown may keep
// cleaning up in the background because regular-file writes are not
// cancellable on every supported platform.
func (r *AsyncRecorder) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.beginShutdown()
	select {
	case <-r.done:
		return r.terminalError()
	case <-ctx.Done():
		return errors.Join(ctx.Err(), r.terminalError())
	}
}

func (r *AsyncRecorder) Close() error {
	if r == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAsyncCloseTimeout)
	defer cancel()
	return r.Shutdown(ctx)
}

func (r *AsyncRecorder) Stats() AsyncStats {
	if r == nil {
		return AsyncStats{}
	}
	return AsyncStats{
		Accepted: r.accepted.Load(),
		Written:  r.written.Load(),
		Failed:   r.failed.Load(),
		Rejected: r.rejected.Load(),
	}
}

func (r *AsyncRecorder) beginShutdown() {
	r.shutdownOnce.Do(func() {
		r.stateMu.Lock()
		r.stopping = true
		close(r.stop)
		r.stateMu.Unlock()
		go func() {
			r.producers.Wait()
			close(r.queue)
		}()
	})
}

func (r *AsyncRecorder) run() {
	failedMode := r.recorder == nil
	preparedSink, hasPreparedSink := r.recorder.(interface {
		recordPrepared(context.Context, Event) error
	})
	if failedMode {
		r.beginShutdown()
	}
	for event := range r.queue {
		if failedMode {
			r.failed.Add(1)
			continue
		}
		var err error
		if hasPreparedSink {
			err = preparedSink.recordPrepared(context.Background(), event)
		} else {
			err = r.recorder.Record(context.Background(), event)
		}
		if err != nil {
			r.failed.Add(1)
			r.setFirstWriteError(fmt.Errorf("write telemetry event: %w", err))
			failedMode = true
			r.beginShutdown()
			continue
		}
		r.written.Add(1)
	}
	if closer, ok := r.recorder.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			r.setCloseError(fmt.Errorf("close telemetry recorder: %w", err))
		}
	}
	close(r.done)
}

func (r *AsyncRecorder) setFirstWriteError(err error) {
	if err == nil {
		return
	}
	r.errMu.Lock()
	if r.firstWriteErr == nil {
		r.firstWriteErr = err
	}
	r.errMu.Unlock()
}

func (r *AsyncRecorder) setCloseError(err error) {
	if err == nil {
		return
	}
	r.errMu.Lock()
	r.closeErr = errors.Join(r.closeErr, err)
	r.errMu.Unlock()
}

func (r *AsyncRecorder) terminalError() error {
	if r == nil {
		return ErrRecorderClosed
	}
	r.errMu.RLock()
	defer r.errMu.RUnlock()
	return errors.Join(r.firstWriteErr, r.closeErr)
}

func BuildReportFromJSONL(path string) (Report, error) {
	return BuildReportFromJSONLWithLimits(path, DefaultReportLimits())
}

func BuildReportFromJSONLWithLimits(path string, limits ReportLimits) (report Report, returnErr error) {
	path = normalizePath(path)
	dir, name, err := splitTelemetryPath(path)
	if err != nil {
		return Report{}, err
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return Report{}, err
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return Report{}, fmt.Errorf("telemetry directory must be a real directory, not a symlink")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Report{}, err
	}
	openedDirInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(dirInfo, openedDirInfo) {
		_ = root.Close()
		if err != nil {
			return Report{}, fmt.Errorf("verify opened telemetry root: %w", err)
		}
		return Report{}, fmt.Errorf("telemetry directory changed while opening")
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()

	caseInsensitive := telemetryFilesystemIsCaseInsensitive(dir)
	for attempt := 1; attempt <= maxTelemetrySnapshotAttempts; attempt++ {
		report, err = buildReportFromTelemetrySnapshot(root, name, path, limits, caseInsensitive)
		if !errors.Is(err, errTelemetrySnapshotChanged) {
			if errors.Is(err, os.ErrNotExist) {
				return Report{}, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
			}
			return report, err
		}
	}
	return Report{}, fmt.Errorf("telemetry rotated during %d report snapshot attempts: %w", maxTelemetrySnapshotAttempts, errTelemetrySnapshotChanged)
}

type telemetrySnapshotFile struct {
	name string
	file *os.File
	info os.FileInfo
	size int64
}

func buildReportFromTelemetrySnapshot(root *os.Root, activeName, source string, limits ReportLimits, caseInsensitive bool) (Report, error) {
	segments, err := listTelemetrySegments(root, activeName, caseInsensitive)
	if err != nil {
		return Report{}, err
	}

	files := make([]telemetrySnapshotFile, 0, len(segments))
	closeFiles := func() error {
		var closeErr error
		for _, snapshot := range files {
			closeErr = errors.Join(closeErr, snapshot.file.Close())
		}
		return closeErr
	}
	for _, segment := range segments {
		file, info, openErr := openSecureRegularFile(root, segment, os.O_RDONLY, false)
		if openErr != nil {
			closeErr := closeFiles()
			if errors.Is(openErr, os.ErrNotExist) {
				return Report{}, errors.Join(errTelemetrySnapshotChanged, closeErr)
			}
			return Report{}, errors.Join(fmt.Errorf("open telemetry segment %q: %w", segment, openErr), closeErr)
		}
		files = append(files, telemetrySnapshotFile{name: segment, file: file, info: info, size: info.Size()})
	}

	if err := verifyTelemetrySnapshot(root, activeName, segments, files, caseInsensitive); err != nil {
		return Report{}, errors.Join(err, closeFiles())
	}

	builder := NewReportBuilderWithLimits(source, limits)
	for _, snapshot := range files {
		if err := addJSONLToReport(io.LimitReader(snapshot.file, snapshot.size), builder); err != nil {
			return Report{}, errors.Join(fmt.Errorf("read telemetry segment %q: %w", snapshot.name, err), closeFiles())
		}
	}
	if err := closeFiles(); err != nil {
		return Report{}, err
	}
	return builder.Report(), nil
}

func verifyTelemetrySnapshot(root *os.Root, activeName string, segments []string, files []telemetrySnapshotFile, caseInsensitive bool) error {
	current, err := listTelemetrySegments(root, activeName, caseInsensitive)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errTelemetrySnapshotChanged
		}
		return err
	}
	if len(current) != len(segments) {
		return errTelemetrySnapshotChanged
	}
	for index := range segments {
		if current[index] != segments[index] {
			return errTelemetrySnapshotChanged
		}
		info, statErr := root.Lstat(segments[index])
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return errTelemetrySnapshotChanged
			}
			return statErr
		}
		if err := validateRegularFileInfo(info); err != nil {
			return err
		}
		if !os.SameFile(info, files[index].info) {
			return errTelemetrySnapshotChanged
		}
	}
	return nil
}

type telemetrySegment struct {
	name  string
	index int
}

func listTelemetrySegments(root *os.Root, activeName string, caseInsensitive bool) ([]string, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	backups := make([]telemetrySegment, 0, DefaultMaxBackups)
	activeSegment := ""
	for {
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			if telemetryNameEqual(entry.Name(), activeName, caseInsensitive) {
				activeSegment = entry.Name()
				continue
			}
			index, isBackup := telemetryBackupIndex(entry.Name(), activeName, caseInsensitive)
			if !isBackup {
				continue
			}
			if len(backups) >= maxTelemetrySegments {
				_ = directory.Close()
				return nil, fmt.Errorf("too many telemetry segments: maximum is %d", maxTelemetrySegments)
			}
			backups = append(backups, telemetrySegment{name: entry.Name(), index: index})
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = directory.Close()
			return nil, readErr
		}
	}
	if err := directory.Close(); err != nil {
		return nil, err
	}
	if activeSegment == "" && len(backups) == 0 {
		return nil, &os.PathError{Op: "open", Path: activeName, Err: os.ErrNotExist}
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].index != backups[j].index {
			return backups[i].index > backups[j].index
		}
		return backups[i].name < backups[j].name
	})
	segments := make([]string, 0, len(backups)+1)
	for _, backup := range backups {
		segments = append(segments, backup.name)
	}
	if activeSegment != "" {
		segments = append(segments, activeSegment)
	}
	return segments, nil
}

func telemetryNameEqual(left, right string, caseInsensitive bool) bool {
	if caseInsensitive {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func telemetryBackupIndex(candidate, activeName string, caseInsensitive bool) (int, bool) {
	dot := strings.LastIndexByte(candidate, '.')
	if dot <= 0 || !telemetryNameEqual(candidate[:dot], activeName, caseInsensitive) {
		return 0, false
	}
	suffix := candidate[dot+1:]
	index, err := strconv.Atoi(suffix)
	if err != nil || index <= 0 || strconv.Itoa(index) != suffix {
		return 0, false
	}
	return index, true
}

func BuildReportFromReader(r io.Reader, source string) (Report, error) {
	return BuildReportFromReaderWithLimits(r, source, DefaultReportLimits())
}

func BuildReportFromReaderWithLimits(r io.Reader, source string, limits ReportLimits) (Report, error) {
	builder := NewReportBuilderWithLimits(source, limits)
	err := addJSONLToReport(r, builder)
	if err != nil {
		return Report{}, err
	}
	return builder.Report(), nil
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return DefaultFile
	}
	return path
}

type Report struct {
	Source                            string            `json:"source"`
	TotalCalls                        uint64            `json:"total_calls"`
	SuccessfulCalls                   uint64            `json:"successful_calls"`
	FailedCalls                       uint64            `json:"failed_calls"`
	DeliveryFailures                  uint64            `json:"delivery_failures"`
	ErrorRate                         float64           `json:"error_rate"`
	RequestBytes                      uint64            `json:"request_bytes"`
	ResponseBytes                     uint64            `json:"response_bytes"`
	EstimatedInputTokens              uint64            `json:"estimated_input_tokens"`
	EstimatedOutputTokens             uint64            `json:"estimated_output_tokens"`
	LatencyUS                         LatencyStats      `json:"latency_us"`
	LatencyMS                         LatencyStats      `json:"latency_ms"`
	LatencyPercentileMaxRelativeError float64           `json:"latency_percentile_max_relative_error"`
	Tools                             []GroupStats      `json:"tools"`
	Clients                           []GroupStats      `json:"clients"`
	Sessions                          []GroupStats      `json:"sessions"`
	Transports                        []GroupStats      `json:"transports"`
	Diagnostics                       ReportDiagnostics `json:"diagnostics"`
}

type ReportDiagnostics struct {
	InvalidLines           uint64 `json:"invalid_lines"`
	TruncatedLines         uint64 `json:"truncated_lines"`
	TruncatedLabels        uint64 `json:"truncated_labels"`
	RedactedIdentityFields uint64 `json:"redacted_identity_fields"`
	LegacyEvents           uint64 `json:"legacy_events"`
	UnsupportedVersions    uint64 `json:"unsupported_versions"`
	GroupLimitOverflows    uint64 `json:"group_limit_overflows"`
	OverflowedValues       uint64 `json:"overflowed_values"`
}

type ReportLimits struct {
	Tools      int
	Clients    int
	Sessions   int
	Transports int
}

func DefaultReportLimits() ReportLimits {
	return ReportLimits{Tools: 128, Clients: 256, Sessions: 256, Transports: 16}
}

type LatencyStats struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	Max int64 `json:"max"`
}

type GroupStats struct {
	Name                  string       `json:"name"`
	Overflow              bool         `json:"overflow,omitempty"`
	Calls                 uint64       `json:"calls"`
	FailedCalls           uint64       `json:"failed_calls"`
	DeliveryFailures      uint64       `json:"delivery_failures"`
	ErrorRate             float64      `json:"error_rate"`
	RequestBytes          uint64       `json:"request_bytes"`
	ResponseBytes         uint64       `json:"response_bytes"`
	EstimatedInputTokens  uint64       `json:"estimated_input_tokens"`
	EstimatedOutputTokens uint64       `json:"estimated_output_tokens"`
	LatencyUS             LatencyStats `json:"latency_us"`
	LatencyMS             LatencyStats `json:"latency_ms"`
}

type groupAccumulator struct {
	stats   GroupStats
	latency latencyHistogram
}

type boundedGroups struct {
	limit    int
	groups   map[string]*groupAccumulator
	overflow *groupAccumulator
}

type ReportBuilder struct {
	report          Report
	toolGroups      boundedGroups
	clientGroups    boundedGroups
	sessionGroups   boundedGroups
	transportGroups boundedGroups
	latency         latencyHistogram
}

func NewReportBuilder(source string) *ReportBuilder {
	return NewReportBuilderWithLimits(source, DefaultReportLimits())
}

func NewReportBuilderWithLimits(source string, limits ReportLimits) *ReportBuilder {
	limits = normalizeReportLimits(limits)
	return &ReportBuilder{
		report: Report{
			Source:                            source,
			LatencyPercentileMaxRelativeError: 1.0 / latencySubBuckets,
		},
		toolGroups:      newBoundedGroups(limits.Tools),
		clientGroups:    newBoundedGroups(limits.Clients),
		sessionGroups:   newBoundedGroups(limits.Sessions),
		transportGroups: newBoundedGroups(limits.Transports),
	}
}

func BuildReport(events []Event, source string) Report {
	builder := NewReportBuilder(source)
	for _, event := range events {
		builder.Add(event)
	}
	return builder.Report()
}

func (b *ReportBuilder) Add(event Event) {
	if b == nil {
		return
	}
	prepared, truncated, err := normalizeEventForWrite(event)
	if err != nil {
		noteDiagnostic(&b.report.Diagnostics.InvalidLines)
		return
	}
	b.addPrepared(prepared, truncated)
}

func (b *ReportBuilder) addPrepared(event Event, truncatedLabels uint64) {
	checkedAdd(&b.report.TotalCalls, 1, &b.report.Diagnostics)
	if event.Success {
		checkedAdd(&b.report.SuccessfulCalls, 1, &b.report.Diagnostics)
	} else {
		checkedAdd(&b.report.FailedCalls, 1, &b.report.Diagnostics)
	}
	if event.DeliveryError {
		checkedAdd(&b.report.DeliveryFailures, 1, &b.report.Diagnostics)
	}
	checkedAdd(&b.report.RequestBytes, uint64(event.RequestBytes), &b.report.Diagnostics)
	checkedAdd(&b.report.ResponseBytes, uint64(event.ResponseBytes), &b.report.Diagnostics)
	checkedAdd(&b.report.EstimatedInputTokens, uint64(event.EstimatedInputTokens), &b.report.Diagnostics)
	checkedAdd(&b.report.EstimatedOutputTokens, uint64(event.EstimatedOutputTokens), &b.report.Diagnostics)
	checkedAdd(&b.report.Diagnostics.TruncatedLabels, truncatedLabels, &b.report.Diagnostics)
	if b.latency.add(event.DurationUS) {
		noteDiagnostic(&b.report.Diagnostics.OverflowedValues)
	}

	b.toolGroups.add(event.ToolName, event, &b.report.Diagnostics)
	if client := ClientLabel(event); client != "" {
		b.clientGroups.add(client, event, &b.report.Diagnostics)
	}
	if event.SessionID != "" {
		b.sessionGroups.add(event.SessionID, event, &b.report.Diagnostics)
	}
	b.transportGroups.add(event.Transport, event, &b.report.Diagnostics)
}

func (b *ReportBuilder) Report() Report {
	if b == nil {
		return Report{}
	}
	report := b.report
	report.ErrorRate = errorRate(report.FailedCalls, report.TotalCalls)
	report.LatencyUS = b.latency.stats()
	report.LatencyMS = latencyStatsToMillis(report.LatencyUS)
	report.Tools = b.toolGroups.finalize()
	report.Clients = b.clientGroups.finalize()
	report.Sessions = b.sessionGroups.finalize()
	report.Transports = b.transportGroups.finalize()
	return report
}

func ClientLabel(event Event) string {
	name := strings.TrimSpace(event.ClientName)
	version := strings.TrimSpace(event.ClientVersion)
	if name != "" && version != "" {
		return name + "@" + version
	}
	if name != "" {
		return name
	}
	userAgent := strings.TrimSpace(event.UserAgent)
	if userAgent != "" {
		return userAgent
	}
	return ""
}

func EstimateTokensFromBytes(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	tokens := bytes / tokenByteSize
	if bytes%tokenByteSize != 0 {
		tokens++
	}
	return tokens
}

func normalizeEventForWrite(event Event) (Event, uint64, error) {
	if event.DurationUS < 0 || event.DurationMS < 0 || event.RequestBytes < 0 || event.ResponseBytes < 0 ||
		event.EstimatedInputTokens < 0 || event.EstimatedOutputTokens < 0 {
		return Event{}, 0, fmt.Errorf("invalid telemetry event: numeric fields must be non-negative")
	}
	if event.DurationUS == 0 && event.DurationMS > 0 {
		if event.DurationMS > mathMaxInt64/1000 {
			return Event{}, 0, fmt.Errorf("invalid telemetry event: duration overflows microseconds")
		}
		event.DurationUS = event.DurationMS * 1000
	}
	event.DurationMS = event.DurationUS / 1000
	event.Version = eventVersion
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}

	var truncated uint64
	event.SessionID, truncated = sanitizeAndCount(event.SessionID, truncated)
	event.ClientName, truncated = sanitizeAndCount(event.ClientName, truncated)
	event.ClientVersion, truncated = sanitizeAndCount(event.ClientVersion, truncated)
	event.UserAgent, truncated = sanitizeAndCount(event.UserAgent, truncated)
	event.Transport, truncated = sanitizeAndCount(event.Transport, truncated)
	event.Transport = strings.ToLower(event.Transport)
	if event.Transport != "stdio" && event.Transport != "http" {
		return Event{}, 0, fmt.Errorf("invalid telemetry event: transport must be stdio or http")
	}
	event.ToolName, truncated = sanitizeAndCount(event.ToolName, truncated)
	if event.ToolName == "" {
		event.ToolName = "unknown"
	}
	if event.Transport == "stdio" && event.SessionID == "" {
		return Event{}, 0, fmt.Errorf("invalid telemetry event: session_id is required for stdio")
	}
	if event.Transport == "http" {
		event.SessionID = ""
		event.ClientName = ""
		event.ClientVersion = ""
		event.UserAgent = ""
	}

	event.EstimatedInputTokens = EstimateTokensFromBytes(event.RequestBytes)
	event.EstimatedOutputTokens = EstimateTokensFromBytes(event.ResponseBytes)
	if event.Success {
		event.ErrorCode = 0
		event.ErrorKind = ""
	} else {
		event.ErrorKind, truncated = sanitizeAndCount(event.ErrorKind, truncated)
		if event.ErrorKind == "" {
			event.ErrorKind = "tool_error"
		}
	}
	return event, truncated, nil
}

const mathMaxInt64 = int64(^uint64(0) >> 1)

func sanitizeAndCount(value string, count uint64) (string, uint64) {
	sanitized, truncated := sanitizeOptionalLabel(value)
	if truncated && count < ^uint64(0) {
		count++
	}
	return sanitized, count
}

func SanitizeLabel(value, fallback string) string {
	value = SanitizeOptionalLabel(value)
	if value == "" {
		return fallback
	}
	return value
}

func SanitizeOptionalLabel(value string) string {
	value, _ = sanitizeOptionalLabel(value)
	return value
}

func sanitizeOptionalLabel(value string) (string, bool) {
	if labelIsCanonical(value) {
		return value, false
	}
	var b strings.Builder
	grow := len(value)
	if max := MaxLabelRunes*utf8.UTFMax + len("..."); grow > max {
		grow = max
	}
	b.Grow(grow)

	count := 0
	pendingSpace := false
	wrote := false
	for _, r := range value {
		if unsafeLabelRune(r) {
			if wrote {
				pendingSpace = true
			}
			continue
		}
		if pendingSpace {
			if count >= MaxLabelRunes {
				b.WriteString("...")
				return b.String(), true
			}
			b.WriteByte(' ')
			count++
			pendingSpace = false
		}
		if count >= MaxLabelRunes {
			b.WriteString("...")
			return b.String(), true
		}
		b.WriteRune(r)
		count++
		wrote = true
	}
	return b.String(), false
}

func labelIsCanonical(value string) bool {
	if value == "" {
		return true
	}
	runeCount := 0
	previousSpace := false
	for offset := 0; offset < len(value); {
		r, size := utf8.DecodeRuneInString(value[offset:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if unsafeLabelRune(r) {
			if r != ' ' || offset == 0 || offset+size == len(value) || previousSpace {
				return false
			}
			previousSpace = true
		} else {
			previousSpace = false
		}
		runeCount++
		if runeCount > MaxLabelRunes {
			return false
		}
		offset += size
	}
	return true
}

func unsafeLabelRune(r rune) bool {
	return unicode.IsControl(r) || unicode.IsSpace(r) ||
		r == '\u061c' || r == '\u200e' || r == '\u200f' ||
		(r >= '\u202a' && r <= '\u202e') ||
		(r >= '\u2066' && r <= '\u206f')
}

func normalizeReportLimits(limits ReportLimits) ReportLimits {
	limits.Tools = clampReportLimit(limits.Tools)
	limits.Clients = clampReportLimit(limits.Clients)
	limits.Sessions = clampReportLimit(limits.Sessions)
	limits.Transports = clampReportLimit(limits.Transports)
	return limits
}

func clampReportLimit(limit int) int {
	if limit < 0 {
		return 0
	}
	if limit > maxReportGroupLimit {
		return maxReportGroupLimit
	}
	return limit
}

func newBoundedGroups(limit int) boundedGroups {
	return boundedGroups{limit: limit, groups: make(map[string]*groupAccumulator, min(limit, 16))}
}

func (g *boundedGroups) add(name string, event Event, diagnostics *ReportDiagnostics) {
	if name == "" {
		name = "unknown"
	}
	group := g.groups[name]
	if group == nil {
		if len(g.groups) < g.limit {
			group = &groupAccumulator{stats: GroupStats{Name: name}}
			g.groups[name] = group
		} else {
			if g.overflow == nil {
				g.overflow = &groupAccumulator{stats: GroupStats{Name: "other", Overflow: true}}
				if diagnostics != nil {
					noteDiagnostic(&diagnostics.GroupLimitOverflows)
				}
			}
			group = g.overflow
		}
	}
	group.add(event, diagnostics)
}

func (g *boundedGroups) finalize() []GroupStats {
	capacity := len(g.groups)
	if g.overflow != nil {
		capacity++
	}
	out := make([]GroupStats, 0, capacity)
	for _, group := range g.groups {
		out = append(out, group.finalize())
	}
	if g.overflow != nil {
		out = append(out, g.overflow.finalize())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		if out[i].Overflow != out[j].Overflow {
			return !out[i].Overflow
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (g *groupAccumulator) add(event Event, diagnostics *ReportDiagnostics) {
	checkedAdd(&g.stats.Calls, 1, diagnostics)
	if !event.Success {
		checkedAdd(&g.stats.FailedCalls, 1, diagnostics)
	}
	if event.DeliveryError {
		checkedAdd(&g.stats.DeliveryFailures, 1, diagnostics)
	}
	checkedAdd(&g.stats.RequestBytes, uint64(event.RequestBytes), diagnostics)
	checkedAdd(&g.stats.ResponseBytes, uint64(event.ResponseBytes), diagnostics)
	checkedAdd(&g.stats.EstimatedInputTokens, uint64(event.EstimatedInputTokens), diagnostics)
	checkedAdd(&g.stats.EstimatedOutputTokens, uint64(event.EstimatedOutputTokens), diagnostics)
	if g.latency.add(event.DurationUS) {
		if diagnostics != nil {
			noteDiagnostic(&diagnostics.OverflowedValues)
		}
	}
}

func (g *groupAccumulator) finalize() GroupStats {
	stats := g.stats
	stats.ErrorRate = errorRate(stats.FailedCalls, stats.Calls)
	stats.LatencyUS = g.latency.stats()
	stats.LatencyMS = latencyStatsToMillis(stats.LatencyUS)
	return stats
}

type latencyHistogram struct {
	counts [latencyBucketCount]uint64
	total  uint64
	max    int64
}

func (h *latencyHistogram) add(value int64) bool {
	if value < 0 {
		value = 0
	}
	index := latencyBucketIndex(value)
	if h.counts[index] == ^uint64(0) || h.total == ^uint64(0) {
		return true
	}
	h.counts[index]++
	h.total++
	if value > h.max {
		h.max = value
	}
	return false
}

func (h *latencyHistogram) stats() LatencyStats {
	if h == nil || h.total == 0 {
		return LatencyStats{}
	}
	return LatencyStats{
		P50: h.percentile(50),
		P95: h.percentile(95),
		Max: h.max,
	}
}

func (h *latencyHistogram) percentile(percent uint64) int64 {
	target := percentileTarget(h.total, percent)
	var seen uint64
	for index, count := range h.counts {
		if count > ^uint64(0)-seen {
			return latencyBucketUpper(index)
		}
		seen += count
		if seen >= target {
			return latencyBucketUpper(index)
		}
	}
	return h.max
}

func latencyBucketIndex(value int64) int {
	if value < latencyExactBuckets {
		return int(value)
	}
	u := uint64(value)
	exponent := bits.Len64(u) - 1
	base := uint64(1) << exponent
	step := base / latencySubBuckets
	offset := int((u - base) / step)
	if offset >= latencySubBuckets {
		offset = latencySubBuckets - 1
	}
	return latencyExactBuckets + (exponent-4)*latencySubBuckets + offset
}

func latencyBucketUpper(index int) int64 {
	if index < latencyExactBuckets {
		return int64(index)
	}
	adjusted := index - latencyExactBuckets
	exponent := adjusted/latencySubBuckets + 4
	offset := adjusted % latencySubBuckets
	base := uint64(1) << exponent
	step := base / latencySubBuckets
	upper := base + uint64(offset+1)*step - 1
	if upper > uint64(mathMaxInt64) {
		return mathMaxInt64
	}
	return int64(upper)
}

func percentileTarget(total, percent uint64) uint64 {
	if total == 0 {
		return 0
	}
	target := (total/100)*percent + ((total%100)*percent+99)/100
	if target == 0 {
		return 1
	}
	return target
}

func latencyStatsToMillis(stats LatencyStats) LatencyStats {
	return LatencyStats{P50: stats.P50 / 1000, P95: stats.P95 / 1000, Max: stats.Max / 1000}
}

func errorRate(failures, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(failures) / float64(total)
}

func checkedAdd(dst *uint64, value uint64, diagnostics *ReportDiagnostics) {
	if value > ^uint64(0)-*dst {
		*dst = ^uint64(0)
		if diagnostics != nil {
			noteDiagnostic(&diagnostics.OverflowedValues)
		}
		return
	}
	*dst += value
}

func noteDiagnostic(counter *uint64) {
	if counter != nil && *counter < ^uint64(0) {
		*counter++
	}
}

type eventWire struct {
	Version               int       `json:"version"`
	Timestamp             time.Time `json:"timestamp"`
	SessionID             string    `json:"session_id"`
	ClientName            string    `json:"client_name"`
	ClientVersion         string    `json:"client_version"`
	UserAgent             string    `json:"user_agent"`
	Transport             *string   `json:"transport"`
	ToolName              *string   `json:"tool_name"`
	Success               *bool     `json:"success"`
	DeliveryError         *bool     `json:"delivery_error"`
	ErrorCode             int       `json:"error_code"`
	ErrorKind             string    `json:"error_kind"`
	DurationUS            *int64    `json:"duration_us"`
	DurationMS            *int64    `json:"duration_ms"`
	RequestBytes          *int      `json:"request_bytes"`
	ResponseBytes         *int      `json:"response_bytes"`
	EstimatedInputTokens  *int      `json:"estimated_input_tokens"`
	EstimatedOutputTokens *int      `json:"estimated_output_tokens"`
}

type decodedEventDiagnostics struct {
	truncatedLabels        uint64
	redactedIdentityFields uint64
}

func decodeEventStrict(line []byte) (Event, decodedEventDiagnostics, error) {
	var diagnostics decodedEventDiagnostics
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Event{}, diagnostics, fmt.Errorf("empty telemetry event")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var wire eventWire
	if err := decoder.Decode(&wire); err != nil {
		return Event{}, diagnostics, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Event{}, diagnostics, fmt.Errorf("multiple JSON values")
		}
		return Event{}, diagnostics, err
	}
	if wire.Timestamp.IsZero() {
		return Event{}, diagnostics, fmt.Errorf("timestamp is required")
	}
	if wire.Version != legacyEventVersion && wire.Version != eventVersion {
		return Event{}, diagnostics, fmt.Errorf("%w: %d", errUnsupportedEventVersion, wire.Version)
	}
	if wire.Transport == nil || wire.ToolName == nil || wire.Success == nil ||
		wire.RequestBytes == nil || wire.ResponseBytes == nil ||
		wire.EstimatedInputTokens == nil || wire.EstimatedOutputTokens == nil {
		return Event{}, diagnostics, fmt.Errorf("transport, tool_name, success, payload bytes, and token estimates are required")
	}
	if *wire.RequestBytes < 0 || *wire.ResponseBytes < 0 || *wire.EstimatedInputTokens < 0 || *wire.EstimatedOutputTokens < 0 {
		return Event{}, diagnostics, fmt.Errorf("numeric fields must be non-negative")
	}
	if *wire.EstimatedInputTokens != EstimateTokensFromBytes(*wire.RequestBytes) ||
		*wire.EstimatedOutputTokens != EstimateTokensFromBytes(*wire.ResponseBytes) {
		return Event{}, diagnostics, fmt.Errorf("token estimates are inconsistent with payload bytes")
	}

	var durationUS int64
	switch wire.Version {
	case legacyEventVersion:
		if wire.DurationMS == nil || wire.DurationUS != nil || wire.DeliveryError != nil {
			return Event{}, diagnostics, fmt.Errorf("invalid v1 telemetry duration or delivery fields")
		}
		if *wire.DurationMS < 0 || *wire.DurationMS > mathMaxInt64/1000 {
			return Event{}, diagnostics, fmt.Errorf("v1 duration_ms is invalid or overflows microseconds")
		}
		durationUS = *wire.DurationMS * 1000
	case eventVersion:
		if wire.DurationUS == nil || *wire.DurationUS < 0 {
			return Event{}, diagnostics, fmt.Errorf("v2 duration_us is required and must be non-negative")
		}
		durationUS = *wire.DurationUS
		if wire.DurationMS != nil && *wire.DurationMS != durationUS/1000 {
			return Event{}, diagnostics, fmt.Errorf("duration_ms is inconsistent with duration_us")
		}
	default:
		return Event{}, diagnostics, fmt.Errorf("%w: %d", errUnsupportedEventVersion, wire.Version)
	}

	deliveryError := wire.DeliveryError != nil && *wire.DeliveryError
	event := Event{
		Version: wire.Version, Timestamp: wire.Timestamp.UTC(),
		SessionID: wire.SessionID, ClientName: wire.ClientName, ClientVersion: wire.ClientVersion,
		UserAgent: wire.UserAgent, Transport: *wire.Transport, ToolName: *wire.ToolName,
		Success: *wire.Success, DeliveryError: deliveryError,
		ErrorCode: wire.ErrorCode, ErrorKind: wire.ErrorKind,
		DurationUS: durationUS, DurationMS: durationUS / 1000,
		RequestBytes: *wire.RequestBytes, ResponseBytes: *wire.ResponseBytes,
		EstimatedInputTokens: *wire.EstimatedInputTokens, EstimatedOutputTokens: *wire.EstimatedOutputTokens,
	}
	event.SessionID, diagnostics.truncatedLabels = sanitizeAndCount(event.SessionID, diagnostics.truncatedLabels)
	event.ClientName, diagnostics.truncatedLabels = sanitizeAndCount(event.ClientName, diagnostics.truncatedLabels)
	event.ClientVersion, diagnostics.truncatedLabels = sanitizeAndCount(event.ClientVersion, diagnostics.truncatedLabels)
	event.UserAgent, diagnostics.truncatedLabels = sanitizeAndCount(event.UserAgent, diagnostics.truncatedLabels)
	event.Transport, diagnostics.truncatedLabels = sanitizeAndCount(event.Transport, diagnostics.truncatedLabels)
	event.ToolName, diagnostics.truncatedLabels = sanitizeAndCount(event.ToolName, diagnostics.truncatedLabels)
	if event.Transport != "stdio" && event.Transport != "http" {
		return Event{}, diagnostics, fmt.Errorf("transport must be stdio or http")
	}
	if event.ToolName == "" {
		return Event{}, diagnostics, fmt.Errorf("tool_name must not be empty")
	}
	if event.Transport == "stdio" && event.SessionID == "" {
		return Event{}, diagnostics, fmt.Errorf("session_id is required for stdio telemetry")
	}
	if event.Transport == "http" {
		for _, value := range []string{event.SessionID, event.ClientName, event.ClientVersion, event.UserAgent} {
			if value != "" {
				noteDiagnostic(&diagnostics.redactedIdentityFields)
			}
		}
		event.SessionID = ""
		event.ClientName = ""
		event.ClientVersion = ""
		event.UserAgent = ""
	}
	if event.Success {
		if event.ErrorCode != 0 || strings.TrimSpace(event.ErrorKind) != "" {
			return Event{}, diagnostics, fmt.Errorf("successful event must not contain error fields")
		}
	} else {
		event.ErrorKind, diagnostics.truncatedLabels = sanitizeAndCount(event.ErrorKind, diagnostics.truncatedLabels)
		if event.ErrorKind == "" {
			return Event{}, diagnostics, fmt.Errorf("failed event requires error_kind")
		}
	}
	return event, diagnostics, nil
}

func addJSONLToReport(r io.Reader, builder *ReportBuilder) error {
	return scanJSONLLines(r, func(line []byte, _ int) error {
		if len(bytes.TrimSpace(line)) == 0 {
			return nil
		}
		event, decodedDiagnostics, err := decodeEventStrict(line)
		if err != nil {
			noteDiagnostic(&builder.report.Diagnostics.InvalidLines)
			if errors.Is(err, errUnsupportedEventVersion) {
				noteDiagnostic(&builder.report.Diagnostics.UnsupportedVersions)
			}
			return nil
		}
		if event.Version == legacyEventVersion {
			noteDiagnostic(&builder.report.Diagnostics.LegacyEvents)
		}
		checkedAdd(&builder.report.Diagnostics.RedactedIdentityFields, decodedDiagnostics.redactedIdentityFields, &builder.report.Diagnostics)
		builder.addPrepared(event, decodedDiagnostics.truncatedLabels)
		return nil
	}, func(_ int) error {
		noteDiagnostic(&builder.report.Diagnostics.InvalidLines)
		noteDiagnostic(&builder.report.Diagnostics.TruncatedLines)
		return nil
	})
}

func scanJSONLLines(r io.Reader, handle func([]byte, int) error, truncated func(int) error) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	lineNo := 0
	var accumulated []byte
	tooLong := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			if !tooLong {
				if len(accumulated)+len(fragment) > MaxJSONLLineBytes {
					tooLong = true
					accumulated = nil
				} else {
					accumulated = append(accumulated, fragment...)
				}
			}
			continue
		}

		if err == nil {
			lineNo++
			if tooLong || len(accumulated)+len(fragment) > MaxJSONLLineBytes {
				if callErr := truncated(lineNo); callErr != nil {
					return callErr
				}
			} else if len(accumulated) == 0 {
				if callErr := handle(fragment[:len(fragment)-1], lineNo); callErr != nil {
					return callErr
				}
			} else {
				accumulated = append(accumulated, fragment[:len(fragment)-1]...)
				if callErr := handle(accumulated, lineNo); callErr != nil {
					return callErr
				}
			}
			accumulated = nil
			tooLong = false
			continue
		}

		if errors.Is(err, io.EOF) {
			if len(fragment) > 0 || len(accumulated) > 0 || tooLong {
				lineNo++
				return truncated(lineNo)
			}
			return nil
		}
		return err
	}
}
