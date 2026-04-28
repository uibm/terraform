package history

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_StartRecording(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.Enable()

	entry, err := manager.StartRecording("apply", []string{"-auto-approve"})
	require.NoError(t, err)
	require.NotNil(t, entry)

	assert.Equal(t, "apply", entry.Command)
	assert.Equal(t, []string{"-auto-approve"}, entry.Arguments)
	assert.Equal(t, tempDir, entry.WorkingDirectory)
	assert.NotEmpty(t, entry.ID)
	assert.False(t, entry.Timestamp.IsZero())
}

func TestManager_FinishRecording(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.Enable()

	// Start recording
	entry, err := manager.StartRecording("apply", []string{"-auto-approve"})
	require.NoError(t, err)

	// Wait a bit to ensure execution time is recorded
	time.Sleep(10 * time.Millisecond)

	// Finish recording
	err = manager.FinishRecording(0, nil)
	require.NoError(t, err)

	// Verify history file was created
	historyPath := filepath.Join(tempDir, HistoryFileName)
	assert.FileExists(t, historyPath)

	// Load and verify content
	data, err := ioutil.ReadFile(historyPath)
	require.NoError(t, err)

	var historyFile HistoryFile
	err = json.Unmarshal(data, &historyFile)
	require.NoError(t, err)

	assert.Equal(t, "1.0", historyFile.Version)
	assert.Len(t, historyFile.Entries, 1)

	recorded := historyFile.Entries[0]
	assert.Equal(t, entry.ID, recorded.ID)
	assert.Equal(t, "apply", recorded.Command)
	assert.NotNil(t, recorded.ExitCode)
	assert.Equal(t, 0, *recorded.ExitCode)
	assert.NotNil(t, recorded.ExecutionTime)
	assert.Greater(t, *recorded.ExecutionTime, 0.0)
}

func TestManager_RecordingDisabled(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	// Keep disabled (default state)

	entry, err := manager.StartRecording("apply", []string{"-auto-approve"})
	assert.NoError(t, err)
	assert.Nil(t, entry)

	err = manager.FinishRecording(0, nil)
	assert.NoError(t, err)

	// Verify no history file was created
	historyPath := filepath.Join(tempDir, HistoryFileName)
	assert.NoFileExists(t, historyPath)
}

func TestSanitizeArguments(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		includeSensitive bool
		expected         []string
	}{
		{
			name:             "no sensitive args",
			args:             []string{"apply", "-auto-approve"},
			includeSensitive: false,
			expected:         []string{"apply", "-auto-approve"},
		},
		{
			name:             "var flag",
			args:             []string{"apply", "-var", "password=secret123"},
			includeSensitive: false,
			expected:         []string{"apply", "-var", "[REDACTED]"},
		},
		{
			name:             "var with equals",
			args:             []string{"apply", "-var=password=secret123"},
			includeSensitive: false,
			expected:         []string{"apply", "-var=[REDACTED]"},
		},
		{
			name:             "include sensitive enabled",
			args:             []string{"apply", "-var", "password=secret123"},
			includeSensitive: true,
			expected:         []string{"apply", "-var", "password=secret123"},
		},
		{
			name:             "multiple sensitive flags",
			args:             []string{"apply", "-var", "pass=secret", "-backend-config", "token=abc123"},
			includeSensitive: false,
			expected:         []string{"apply", "-var", "[REDACTED]", "-backend-config", "[REDACTED]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeArguments(tt.args, tt.includeSensitive)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRetentionPolicy(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.config.Enabled = true
	manager.config.MaxEntries = 3
	manager.config.RetentionDays = 1

	// Create entries with different timestamps
	now := time.Now().UTC()
	entries := []Entry{
		{ID: "1", Timestamp: now.AddDate(0, 0, -2)}, // Too old
		{ID: "2", Timestamp: now.AddDate(0, 0, -1)}, // Boundary
		{ID: "3", Timestamp: now},                   // Recent
		{ID: "4", Timestamp: now},                   // Recent
		{ID: "5", Timestamp: now},                   // Recent
		{ID: "6", Timestamp: now},                   // Recent (should be kept, newest)
	}

	filtered := manager.applyRetentionPolicy(entries)

	// Should keep max 3 entries, all within retention period
	assert.Len(t, filtered, 3)
	assert.Equal(t, "6", filtered[0].ID) // Newest first
	assert.Equal(t, "5", filtered[1].ID)
	assert.Equal(t, "4", filtered[2].ID)
}

func TestConcurrentAccess(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager1 := NewManager(tempDir)
	manager1.Enable()
	manager2 := NewManager(tempDir)
	manager2.Enable()

	// Simulate concurrent recording
	done1 := make(chan bool)
	done2 := make(chan bool)

	go func() {
		defer close(done1)
		entry, err := manager1.StartRecording("apply", []string{"-auto-approve"})
		require.NoError(t, err)
		time.Sleep(50 * time.Millisecond)
		err = manager1.FinishRecording(0, nil)
		require.NoError(t, err)
	}()

	go func() {
		defer close(done2)
		entry, err := manager2.StartRecording("plan", []string{})
		require.NoError(t, err)
		time.Sleep(50 * time.Millisecond)
		err = manager2.FinishRecording(0, nil)
		require.NoError(t, err)
	}()

	// Wait for both to complete
	<-done1
	<-done2

	// Verify both entries were recorded
	entries, err := loadHistoryEntries(tempDir)
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	// Verify one apply and one plan command
	commands := make(map[string]bool)
	for _, entry := range entries {
		commands[entry.Command] = true
	}
	assert.True(t, commands["apply"])
	assert.True(t, commands["plan"])
}

func TestGitInfoCapture(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	// Initialize a git repo
	setupGitRepo(t, tempDir)

	gitInfo := captureGitInfo(tempDir)
	require.NotNil(t, gitInfo)
	assert.Equal(t, "main", gitInfo.Branch)
	assert.NotEmpty(t, gitInfo.Commit)
	assert.False(t, gitInfo.IsDirty) // Clean repo
}

func TestGitInfoCaptureWithChanges(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	// Initialize git repo and make changes
	setupGitRepo(t, tempDir)

	// Create an untracked file
	err := ioutil.WriteFile(filepath.Join(tempDir, "test.tf"), []byte("# test"), 0644)
	require.NoError(t, err)

	gitInfo := captureGitInfo(tempDir)
	require.NotNil(t, gitInfo)
	assert.True(t, gitInfo.IsDirty) // Dirty repo due to untracked file
}

func TestEnvironmentCapture(t *testing.T) {
	// Set test environment variables
	os.Setenv("TF_VAR_test", "value")
	os.Setenv("AWS_PROFILE", "test-profile")
	os.Setenv("IRRELEVANT_VAR", "should-not-capture")
	defer func() {
		os.Unsetenv("TF_VAR_test")
		os.Unsetenv("AWS_PROFILE")
		os.Unsetenv("IRRELEVANT_VAR")
	}()

	env := captureEnvironment()

	assert.Contains(t, env, "TF_VAR_test")
	assert.Equal(t, "value", env["TF_VAR_test"])
	assert.Contains(t, env, "AWS_PROFILE")
	assert.Equal(t, "test-profile", env["AWS_PROFILE"])
	assert.NotContains(t, env, "IRRELEVANT_VAR")
}

func TestStateInfoUpdate(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.Enable()

	// Start recording
	_, err := manager.StartRecording("apply", []string{})
	require.NoError(t, err)

	// Update state info
	stateInfo := &StateInfo{
		BackendType:        "s3",
		StateVersion:       42,
		ResourcesBefore:    5,
		ResourcesAfter:     7,
		ResourcesAdded:     2,
		ResourcesChanged:   1,
		ResourcesDestroyed: 0,
	}
	manager.UpdateStateInfo(stateInfo)

	// Finish recording
	err = manager.FinishRecording(0, nil)
	require.NoError(t, err)

	// Verify state info was recorded
	entries, err := loadHistoryEntries(tempDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, stateInfo, entries[0].StateInfo)
}

func TestPlanSummaryUpdate(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.Enable()

	// Start recording
	_, err := manager.StartRecording("plan", []string{})
	require.NoError(t, err)

	// Update plan summary
	planSummary := &PlanSummary{
		Add:      3,
		Change:   2,
		Destroy:  1,
		HasDrift: true,
	}
	manager.UpdatePlanSummary(planSummary)

	// Finish recording
	err = manager.FinishRecording(0, nil)
	require.NoError(t, err)

	// Verify plan summary was recorded
	entries, err := loadHistoryEntries(tempDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, planSummary, entries[0].PlanSummary)
}

func TestResourceChangeTracking(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.Enable()

	// Start recording
	_, err := manager.StartRecording("apply", []string{})
	require.NoError(t, err)

	// Add resource changes
	changes := []ResourceChange{
		{
			Address:      "aws_instance.web",
			Action:       "create",
			Provider:     "aws",
			ResourceType: "aws_instance",
			ChangeReason: "new resource",
		},
		{
			Address:      "aws_security_group.web",
			Action:       "update",
			Provider:     "aws",
			ResourceType: "aws_security_group",
			ChangeReason: "configuration changed",
		},
	}

	for _, change := range changes {
		manager.AddResourceChange(change)
	}

	// Finish recording
	err = manager.FinishRecording(0, nil)
	require.NoError(t, err)

	// Verify resource changes were recorded
	entries, err := loadHistoryEntries(tempDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, changes, entries[0].ResourcesAffected)
}

func TestErrorRecording(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.Enable()

	// Start recording
	_, err := manager.StartRecording("apply", []string{})
	require.NoError(t, err)

	// Create error details
	errorDetails := &ErrorDetails{
		Message:    "Resource creation failed",
		Type:       "terraform_error",
		Code:       "RESOURCE_ERROR",
		Diagnostic: "Failed to create aws_instance.web: InvalidAMI",
	}

	// Finish recording with error
	err = manager.FinishRecording(1, errorDetails)
	require.NoError(t, err)

	// Verify error was recorded
	entries, err := loadHistoryEntries(tempDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, 1, *entry.ExitCode)
	assert.Equal(t, errorDetails, entry.ErrorDetails)
}

// Command tests

func TestHistoryCommand_List(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	// Create test history
	createTestHistory(t, tempDir)

	// Test list command
	cmd := &HistoryCommand{Meta: testMeta(tempDir)}

	// Capture output
	ui := &testUI{}
	cmd.Ui = ui

	exitCode := cmd.runList([]string{"--limit", "2"})
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, ui.outputs, "apply")
	assert.Contains(t, ui.outputs, "plan")
}

func TestHistoryCommand_Show(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	// Create test history
	entries := createTestHistory(t, tempDir)

	// Test show command
	cmd := &HistoryCommand{Meta: testMeta(tempDir)}

	// Capture output
	ui := &testUI{}
	cmd.Ui = ui

	exitCode := cmd.runShow([]string{entries[0].ID})
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, ui.outputs, "Command ID:")
	assert.Contains(t, ui.outputs, entries[0].ID)
}

func TestHistoryCommand_Export(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	// Create test history
	createTestHistory(t, tempDir)

	// Test export command
	cmd := &HistoryCommand{Meta: testMeta(tempDir)}
	exportPath := filepath.Join(tempDir, "export.json")

	exitCode := cmd.runExport([]string{"--output", exportPath})
	assert.Equal(t, 0, exitCode)
	assert.FileExists(t, exportPath)

	// Verify export content
	data, err := ioutil.ReadFile(exportPath)
	require.NoError(t, err)

	var entries []Entry
	err = json.Unmarshal(data, &entries)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestHistoryCommand_Clean(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	// Create test history with old entries
	createTestHistoryWithOldEntries(t, tempDir)

	// Test clean command
	cmd := &HistoryCommand{Meta: testMeta(tempDir)}

	exitCode := cmd.runClean([]string{"--older-than", "1d", "--force"})
	assert.Equal(t, 0, exitCode)

	// Verify old entries were removed
	entries, err := loadHistoryEntries(tempDir)
	require.NoError(t, err)

	// Should only have recent entries
	for _, entry := range entries {
		assert.True(t, time.Since(entry.Timestamp) < 24*time.Hour)
	}
}

func TestHistoryCommand_EnableDisable(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	cmd := &HistoryCommand{Meta: testMeta(tempDir)}

	// Test enable
	exitCode := cmd.runEnable([]string{})
	assert.Equal(t, 0, exitCode)

	// Verify history is enabled
	manager := NewManager(tempDir)
	// Note: In real implementation, this would check config file
	// For test, we'll verify the history file exists
	historyPath := filepath.Join(tempDir, HistoryFileName)
	assert.FileExists(t, historyPath)

	// Test disable
	exitCode = cmd.runDisable([]string{})
	assert.Equal(t, 0, exitCode)
}

// Integration tests

func TestCommandWrapper_Integration(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	// Enable history
	manager := NewManager(tempDir)
	manager.Enable()

	// Create initial history file
	historyFile := &HistoryFile{
		Version:   "1.0",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Config:    *manager.config,
		Entries:   []Entry{},
	}
	writeTestHistoryFile(t, tempDir, historyFile)

	// Create a mock command
	mockCmd := &mockCommand{exitCode: 0}

	// Wrap with history recording
	wrapper := NewCommandWrapper(mockCmd, "apply", []string{"-auto-approve"})

	// Execute wrapped command
	exitCode := wrapper.Run([]string{"-auto-approve"})
	assert.Equal(t, 0, exitCode)
	assert.True(t, mockCmd.executed)

	// Verify history was recorded
	entries, err := loadHistoryEntries(tempDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "apply", entry.Command)
	assert.Equal(t, []string{"-auto-approve"}, entry.Arguments)
}

func TestHistoryMiddleware_Integration(t *testing.T) {
	tempDir := setupTestDir(t)
	defer os.RemoveAll(tempDir)

	middleware := NewHistoryMiddleware(tempDir)

	// Test command that should be tracked
	mockCmd := &mockCommand{exitCode: 0}
	wrappedCmd := middleware.WrapCommand("apply", mockCmd)

	// Should return wrapped command
	assert.IsType(t, &CommandWrapper{}, wrappedCmd)

	// Test command that should be skipped
	historyCmd := &HistoryCommand{}
	skippedCmd := middleware.WrapCommand("history", historyCmd)

	// Should return original command
	assert.Equal(t, historyCmd, skippedCmd)
}

// Benchmark tests

func BenchmarkHistoryRecording(b *testing.B) {
	tempDir := setupTestDir(b)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.Enable()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry, err := manager.StartRecording("plan", []string{})
		require.NoError(b, err)

		err = manager.FinishRecording(0, nil)
		require.NoError(b, err)
	}
}

func BenchmarkConcurrentRecording(b *testing.B) {
	tempDir := setupTestDir(b)
	defer os.RemoveAll(tempDir)

	manager := NewManager(tempDir)
	manager.Enable()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			entry, err := manager.StartRecording("plan", []string{})
			require.NoError(b, err)

			err = manager.FinishRecording(0, nil)
			require.NoError(b, err)
		}
	})
}

// Helper functions and mocks

func setupTestDir(t testing.TB) string {
	tempDir, err := ioutil.TempDir("", "terraform-history-test")
	require.NoError(t, err)
	return tempDir
}

func setupGitRepo(t *testing.T, dir string) {
	// Initialize git repo
	runCommand(t, dir, "git", "init")
	runCommand(t, dir, "git", "config", "user.email", "test@example.com")
	runCommand(t, dir, "git", "config", "user.name", "Test User")

	// Create initial commit
	err := ioutil.WriteFile(filepath.Join(dir, "main.tf"), []byte("# terraform"), 0644)
	require.NoError(t, err)

	runCommand(t, dir, "git", "add", ".")
	runCommand(t, dir, "git", "commit", "-m", "Initial commit")
}

func runCommand(t *testing.T, dir string, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	err := cmd.Run()
	require.NoError(t, err)
}

func createTestHistory(t *testing.T, dir string) []Entry {
	now := time.Now().UTC()
	entries := []Entry{
		{
			ID:               "test-1",
			Timestamp:        now.Add(-1 * time.Hour),
			Command:          "apply",
			Arguments:        []string{"-auto-approve"},
			WorkingDirectory: dir,
			Workspace:        "production",
			TerraformVersion: "1.8.5",
			ExitCode:         &[]int{0}[0],
			ExecutionTime:    &[]float64{45.2}[0],
			User:             "test-user",
		},
		{
			ID:               "test-2",
			Timestamp:        now.Add(-30 * time.Minute),
			Command:          "plan",
			Arguments:        []string{},
			WorkingDirectory: dir,
			Workspace:        "production",
			TerraformVersion: "1.8.5",
			ExitCode:         &[]int{0}[0],
			ExecutionTime:    &[]float64{12.1}[0],
			User:             "test-user",
		},
	}

	historyFile := &HistoryFile{
		Version:   "1.0",
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now,
		Config:    Config{Enabled: true, MaxEntries: 1000, RetentionDays: 90},
		Entries:   entries,
	}

	writeTestHistoryFile(t, dir, historyFile)
	return entries
}

func createTestHistoryWithOldEntries(t *testing.T, dir string) {
	now := time.Now().UTC()
	entries := []Entry{
		{
			ID:        "old-1",
			Timestamp: now.AddDate(0, 0, -5), // 5 days old
			Command:   "apply",
		},
		{
			ID:        "old-2",
			Timestamp: now.AddDate(0, 0, -2), // 2 days old
			Command:   "plan",
		},
		{
			ID:        "recent-1",
			Timestamp: now.Add(-1 * time.Hour), // 1 hour old
			Command:   "apply",
		},
	}

	historyFile := &HistoryFile{
		Version:   "1.0",
		CreatedAt: now.AddDate(0, 0, -7),
		UpdatedAt: now,
		Config:    Config{Enabled: true, MaxEntries: 1000, RetentionDays: 90},
		Entries:   entries,
	}

	writeTestHistoryFile(t, dir, historyFile)
}

func writeTestHistoryFile(t testing.TB, dir string, historyFile *HistoryFile) {
	data, err := json.MarshalIndent(historyFile, "", "  ")
	require.NoError(t, err)

	historyPath := filepath.Join(dir, HistoryFileName)
	err = ioutil.WriteFile(historyPath, data, 0600)
	require.NoError(t, err)
}

func loadHistoryEntries(dir string) ([]Entry, error) {
	historyPath := filepath.Join(dir, HistoryFileName)
	data, err := ioutil.ReadFile(historyPath)
	if err != nil {
		return nil, err
	}

	var historyFile HistoryFile
	err = json.Unmarshal(data, &historyFile)
	if err != nil {
		return nil, err
	}

	return historyFile.Entries, nil
}

func testMeta(workingDir string) Meta {
	// Return a test Meta instance
	// Implementation depends on actual Meta structure
	return Meta{}
}

type testUI struct {
	outputs []string
	errors  []string
}

func (ui *testUI) Output(message string) {
	ui.outputs = append(ui.outputs, message)
}

func (ui *testUI) Error(message string) {
	ui.errors = append(ui.errors, message)
}

func (ui *testUI) Info(message string) {
	ui.outputs = append(ui.outputs, message)
}

func (ui *testUI) Warn(message string) {
	ui.outputs = append(ui.outputs, message)
}

func (ui *testUI) Ask(query string) (string, error) {
	return "yes", nil
}

func (ui *testUI) AskSecret(query string) (string, error) {
	return "secret", nil
}

type mockCommand struct {
	exitCode int
	executed bool
}

func (mc *mockCommand) Run(args []string) int {
	mc.executed = true
	return mc.exitCode
}

func (mc *mockCommand) Help() string {
	return "Mock command help"
}

func (mc *mockCommand) Synopsis() string {
	return "Mock command"
}
