package history

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform/version"
)

const (
	HistoryFileName      = "terraform.history"
	DefaultMaxEntries    = 1000
	DefaultRetentionDays = 90
	LockFileTimeout      = 30 * time.Second
	LockFileSuffix       = ".lock"
)

// Manager handles all history operations
type Manager struct {
	workingDir   string
	config       *Config
	currentEntry *Entry
	mu           sync.RWMutex
	lockFile     *os.File
}

// Config represents history configuration
type Config struct {
	Enabled          bool `json:"enabled"`
	MaxEntries       int  `json:"max_entries"`
	RetentionDays    int  `json:"retention_days"`
	IncludeSensitive bool `json:"include_sensitive"`
}

// Entry represents a single command execution in history
type Entry struct {
	ID                string            `json:"id"`
	Timestamp         time.Time         `json:"timestamp"`
	Command           string            `json:"command"`
	Arguments         []string          `json:"arguments"`
	SanitizedArgs     []string          `json:"sanitized_arguments"`
	WorkingDirectory  string            `json:"working_directory"`
	Workspace         string            `json:"workspace"`
	TerraformVersion  string            `json:"terraform_version"`
	ExitCode          *int              `json:"exit_code,omitempty"`
	ExecutionTime     *float64          `json:"execution_time,omitempty"`
	StartTime         time.Time         `json:"start_time"`
	EndTime           *time.Time        `json:"end_time,omitempty"`
	User              string            `json:"user"`
	GitInfo           *GitInfo          `json:"git_info,omitempty"`
	StateInfo         *StateInfo        `json:"state_info,omitempty"`
	PlanSummary       *PlanSummary      `json:"plan_summary,omitempty"`
	ResourcesAffected []ResourceChange  `json:"resources_affected,omitempty"`
	ErrorDetails      *ErrorDetails     `json:"error_details,omitempty"`
	Environment       map[string]string `json:"environment,omitempty"`
}

// GitInfo contains git repository information
type GitInfo struct {
	Branch    string `json:"branch,omitempty"`
	Commit    string `json:"commit,omitempty"`
	IsDirty   bool   `json:"is_dirty"`
	RemoteURL string `json:"remote_url,omitempty"`
}

// StateInfo contains Terraform state information
type StateInfo struct {
	BackendType        string `json:"backend_type,omitempty"`
	StateVersion       int    `json:"state_version,omitempty"`
	ResourcesBefore    int    `json:"resources_before,omitempty"`
	ResourcesAfter     int    `json:"resources_after,omitempty"`
	ResourcesAdded     int    `json:"resources_added,omitempty"`
	ResourcesChanged   int    `json:"resources_changed,omitempty"`
	ResourcesDestroyed int    `json:"resources_destroyed,omitempty"`
}

// PlanSummary contains plan execution summary
type PlanSummary struct {
	Add      int  `json:"add"`
	Change   int  `json:"change"`
	Destroy  int  `json:"destroy"`
	HasDrift bool `json:"has_drift"`
}

// ResourceChange represents a change to a specific resource
type ResourceChange struct {
	Address      string            `json:"address"`
	Action       string            `json:"action"` // create, update, delete, read, no-op
	Provider     string            `json:"provider"`
	ResourceType string            `json:"resource_type"`
	ChangeReason string            `json:"change_reason,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

// ErrorDetails contains error information
type ErrorDetails struct {
	Message    string   `json:"message"`
	Type       string   `json:"type"`
	Code       string   `json:"code,omitempty"`
	Diagnostic string   `json:"diagnostic,omitempty"`
	Stacktrace []string `json:"stacktrace,omitempty"`
}

// HistoryFile represents the structure of the history file
type HistoryFile struct {
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Config    Config    `json:"config"`
	Entries   []Entry   `json:"entries"`
}

// NewManager creates a new history manager
func NewManager(workingDir string) *Manager {
	return &Manager{
		workingDir: workingDir,
		config:     getDefaultConfig(),
	}
}

// getDefaultConfig returns the default configuration
func getDefaultConfig() *Config {
	return &Config{
		Enabled:          false, // Disabled by default
		MaxEntries:       DefaultMaxEntries,
		RetentionDays:    DefaultRetentionDays,
		IncludeSensitive: false,
	}
}

// IsEnabled checks if history tracking is enabled
func (m *Manager) IsEnabled() bool {
	if m.config == nil {
		return false
	}
	return m.config.Enabled
}

// Enable enables history tracking
func (m *Manager) Enable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Enabled = true
}

// Disable disables history tracking
func (m *Manager) Disable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Enabled = false
}

// StartRecording begins recording a new command execution
func (m *Manager) StartRecording(command string, args []string) (*Entry, error) {
	if !m.IsEnabled() {
		return nil, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate history ID: %w", err)
	}

	currentUser := getCurrentUser()
	workspace := getCurrentWorkspace(m.workingDir)
	gitInfo := captureGitInfo(m.workingDir)

	entry := &Entry{
		ID:               id,
		Timestamp:        time.Now().UTC(),
		Command:          command,
		Arguments:        args,
		SanitizedArgs:    sanitizeArguments(args, m.config.IncludeSensitive),
		WorkingDirectory: m.workingDir,
		Workspace:        workspace,
		TerraformVersion: version.Version,
		StartTime:        time.Now().UTC(),
		User:             currentUser,
		GitInfo:          gitInfo,
		Environment:      captureEnvironment(),
	}

	m.currentEntry = entry
	return entry, nil
}

// FinishRecording completes the recording of a command execution
func (m *Manager) FinishRecording(exitCode int, errorDetails *ErrorDetails) error {
	if !m.IsEnabled() || m.currentEntry == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	endTime := time.Now().UTC()
	executionTime := endTime.Sub(m.currentEntry.StartTime).Seconds()

	m.currentEntry.ExitCode = &exitCode
	m.currentEntry.EndTime = &endTime
	m.currentEntry.ExecutionTime = &executionTime
	m.currentEntry.ErrorDetails = errorDetails

	// Record the entry to file
	if err := m.recordEntry(m.currentEntry); err != nil {
		return fmt.Errorf("failed to record history entry: %w", err)
	}

	m.currentEntry = nil
	return nil
}

// recordEntry writes an entry to the history file
func (m *Manager) recordEntry(entry *Entry) error {
	historyPath := filepath.Join(m.workingDir, HistoryFileName)

	// Acquire file lock
	if err := m.acquireLock(); err != nil {
		return fmt.Errorf("failed to acquire history file lock: %w", err)
	}
	defer m.releaseLock()

	// Read existing history
	historyFile, err := m.readHistoryFile()
	if err != nil {
		return fmt.Errorf("failed to read history file: %w", err)
	}

	// Add new entry
	historyFile.Entries = append(historyFile.Entries, *entry)
	historyFile.UpdatedAt = time.Now().UTC()

	// Apply retention policy
	historyFile.Entries = m.applyRetentionPolicy(historyFile.Entries)

	// Write back to file
	if err := m.writeHistoryFile(historyFile); err != nil {
		return fmt.Errorf("failed to write history file: %w", err)
	}

	return nil
}

// readHistoryFile reads the history file or creates a new one
func (m *Manager) readHistoryFile() (*HistoryFile, error) {
	historyPath := filepath.Join(m.workingDir, HistoryFileName)

	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		// Create new history file
		return &HistoryFile{
			Version:   "1.0",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Config:    *m.config,
			Entries:   []Entry{},
		}, nil
	}

	data, err := os.ReadFile(historyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read history file: %w", err)
	}

	var historyFile HistoryFile
	if err := json.Unmarshal(data, &historyFile); err != nil {
		return nil, fmt.Errorf("failed to parse history file: %w", err)
	}

	return &historyFile, nil
}

// writeHistoryFile writes the history file to disk
func (m *Manager) writeHistoryFile(historyFile *HistoryFile) error {
	historyPath := filepath.Join(m.workingDir, HistoryFileName)

	data, err := json.MarshalIndent(historyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history file: %w", err)
	}

	// Write to temporary file first
	tempPath := historyPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temporary history file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, historyPath); err != nil {
		os.Remove(tempPath) // Clean up on failure
		return fmt.Errorf("failed to rename history file: %w", err)
	}

	return nil
}

// applyRetentionPolicy applies retention rules to history entries
func (m *Manager) applyRetentionPolicy(entries []Entry) []Entry {
	now := time.Now().UTC()
	cutoffDate := now.AddDate(0, 0, -m.config.RetentionDays)

	// Filter by date
	var filtered []Entry
	for _, entry := range entries {
		if entry.Timestamp.After(cutoffDate) {
			filtered = append(filtered, entry)
		}
	}

	// Sort by timestamp (newest first)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	// Apply max entries limit
	if len(filtered) > m.config.MaxEntries {
		filtered = filtered[:m.config.MaxEntries]
	}

	return filtered
}

// acquireLock acquires a file lock for concurrent access protection
func (m *Manager) acquireLock() error {
	lockPath := filepath.Join(m.workingDir, HistoryFileName+LockFileSuffix)

	// Try to create lock file
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if os.IsExist(err) {
			// Lock file exists, wait and retry
			return m.waitForLock(lockPath)
		}
		return fmt.Errorf("failed to create lock file: %w", err)
	}

	// Write PID to lock file
	pid := os.Getpid()
	if _, err := lockFile.WriteString(strconv.Itoa(pid)); err != nil {
		lockFile.Close()
		os.Remove(lockPath)
		return fmt.Errorf("failed to write to lock file: %w", err)
	}

	m.lockFile = lockFile
	return nil
}

// waitForLock waits for an existing lock to be released
func (m *Manager) waitForLock(lockPath string) error {
	timeout := time.After(LockFileTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for history file lock")
		case <-ticker.C:
			if _, err := os.Stat(lockPath); os.IsNotExist(err) {
				// Lock file is gone, try to acquire it
				return m.acquireLock()
			}
		}
	}
}

// releaseLock releases the file lock
func (m *Manager) releaseLock() {
	if m.lockFile != nil {
		lockPath := m.lockFile.Name()
		m.lockFile.Close()
		os.Remove(lockPath)
		m.lockFile = nil
	}
}

// generateID generates a unique ID for history entries
func generateID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "tf-hist-" + hex.EncodeToString(bytes), nil
}

// getCurrentUser returns the current system user
func getCurrentUser() string {
	if currentUser, err := user.Current(); err == nil {
		return currentUser.Username
	}
	return "unknown"
}

// getCurrentWorkspace returns the current Terraform workspace
func getCurrentWorkspace(workingDir string) string {
	workspaceFile := filepath.Join(workingDir, ".terraform", "environment")
	if data, err := os.ReadFile(workspaceFile); err == nil {
		return strings.TrimSpace(string(data))
	}
	return "default"
}

// captureGitInfo captures git repository information
func captureGitInfo(workingDir string) *GitInfo {
	gitInfo := &GitInfo{}

	// Get current branch
	if branch := getGitOutput(workingDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
		gitInfo.Branch = branch
	}

	// Get current commit
	if commit := getGitOutput(workingDir, "rev-parse", "--short", "HEAD"); commit != "" {
		gitInfo.Commit = commit
	}

	// Check if working tree is dirty
	if status := getGitOutput(workingDir, "status", "--porcelain"); status != "" {
		gitInfo.IsDirty = true
	}

	// Get remote URL
	if remoteURL := getGitOutput(workingDir, "config", "--get", "remote.origin.url"); remoteURL != "" {
		gitInfo.RemoteURL = remoteURL
	}

	// Return nil if no git info was found
	if gitInfo.Branch == "" && gitInfo.Commit == "" {
		return nil
	}

	return gitInfo
}

// getGitOutput executes a git command and returns its output
func getGitOutput(workingDir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = workingDir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// captureEnvironment captures relevant environment variables
func captureEnvironment() map[string]string {
	env := make(map[string]string)

	relevantVars := []string{
		"TF_VAR_.*",
		"AWS_PROFILE",
		"AWS_REGION",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"ARM_SUBSCRIPTION_ID",
		"CI",
		"GITHUB_ACTIONS",
		"GITLAB_CI",
	}

	for _, envVar := range os.Environ() {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key, value := parts[0], parts[1]
		for _, pattern := range relevantVars {
			if matched, _ := filepath.Match(pattern, key); matched {
				env[key] = value
				break
			}
		}
	}

	return env
}

// sanitizeArguments removes sensitive information from command arguments
func sanitizeArguments(args []string, includeSensitive bool) []string {
	if includeSensitive {
		return args
	}

	sensitiveFlags := map[string]bool{
		"-var":            true,
		"-var-file":       true,
		"-backend-config": true,
		"-token":          true,
		"-password":       true,
	}

	sanitized := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Check if this is a sensitive flag
		if sensitiveFlags[arg] && i+1 < len(args) {
			sanitized = append(sanitized, arg)
			sanitized = append(sanitized, "[REDACTED]")
			i++ // Skip the next argument (the sensitive value)
		} else if strings.Contains(arg, "=") {
			// Handle -var=value format
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 && sensitiveFlags[parts[0]] {
				sanitized = append(sanitized, parts[0]+"=[REDACTED]")
			} else {
				sanitized = append(sanitized, arg)
			}
		} else {
			sanitized = append(sanitized, arg)
		}
	}

	return sanitized
}

// UpdateStateInfo updates state information for the current entry
func (m *Manager) UpdateStateInfo(stateInfo *StateInfo) {
	if !m.IsEnabled() || m.currentEntry == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentEntry.StateInfo = stateInfo
}

// UpdatePlanSummary updates plan summary for the current entry
func (m *Manager) UpdatePlanSummary(planSummary *PlanSummary) {
	if !m.IsEnabled() || m.currentEntry == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentEntry.PlanSummary = planSummary
}

// AddResourceChange adds a resource change to the current entry
func (m *Manager) AddResourceChange(change ResourceChange) {
	if !m.IsEnabled() || m.currentEntry == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentEntry.ResourcesAffected == nil {
		m.currentEntry.ResourcesAffected = []ResourceChange{}
	}

	m.currentEntry.ResourcesAffected = append(m.currentEntry.ResourcesAffected, change)
}
