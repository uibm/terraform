// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform/internal/command/history"
)

func TestHistoryHooks_NewHistoryHooks(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)

	if hooks == nil {
		t.Fatal("NewHistoryHooks returned nil")
	}

	if hooks.manager == nil {
		t.Fatal("HistoryHooks manager is nil")
	}
}

func TestHistoryHooks_BeforeAfterCommand(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)

	// Enable history tracking
	hooks.manager.Enable()

	// Test recording a command
	err := hooks.BeforeCommand("plan", []string{})
	if err != nil {
		t.Fatalf("BeforeCommand failed: %s", err)
	}

	if hooks.currentEntry == nil {
		t.Fatal("currentEntry should not be nil after BeforeCommand")
	}

	// Finish recording
	err = hooks.AfterCommand(0)
	if err != nil {
		t.Fatalf("AfterCommand failed: %s", err)
	}

	// Verify history file was created
	historyPath := filepath.Join(tmpDir, history.HistoryFileName)
	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		t.Fatal("History file was not created")
	}

	// Verify history file content
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("Failed to read history file: %s", err)
	}

	var historyFile history.HistoryFile
	if err := json.Unmarshal(data, &historyFile); err != nil {
		t.Fatalf("Failed to parse history file: %s", err)
	}

	if len(historyFile.Entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(historyFile.Entries))
	}

	entry := historyFile.Entries[0]
	if entry.Command != "plan" {
		t.Errorf("Expected command 'plan', got '%s'", entry.Command)
	}

	if entry.ExitCode == nil || *entry.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %v", entry.ExitCode)
	}
}

func TestHistoryHooks_DisabledByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)

	// History should be disabled by default
	if hooks.IsEnabled() {
		t.Error("History should be disabled by default")
	}

	// BeforeCommand should not create entry when disabled
	err := hooks.BeforeCommand("plan", []string{})
	if err != nil {
		t.Fatalf("BeforeCommand failed: %s", err)
	}

	if hooks.currentEntry != nil {
		t.Error("currentEntry should be nil when history is disabled")
	}

	// AfterCommand should not create file when disabled
	err = hooks.AfterCommand(0)
	if err != nil {
		t.Fatalf("AfterCommand failed: %s", err)
	}

	historyPath := filepath.Join(tmpDir, history.HistoryFileName)
	if _, err := os.Stat(historyPath); !os.IsNotExist(err) {
		t.Error("History file should not be created when disabled")
	}
}

func TestHistoryHooks_UpdatePlanSummary(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)
	hooks.manager.Enable()

	// Start recording
	err := hooks.BeforeCommand("plan", []string{})
	if err != nil {
		t.Fatalf("BeforeCommand failed: %s", err)
	}

	// Update plan summary
	hooks.UpdatePlanSummary(5, 3, 2, false)

	// Finish recording
	err = hooks.AfterCommand(0)
	if err != nil {
		t.Fatalf("AfterCommand failed: %s", err)
	}

	// Verify plan summary was recorded
	historyPath := filepath.Join(tmpDir, history.HistoryFileName)
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("Failed to read history file: %s", err)
	}

	var historyFile history.HistoryFile
	if err := json.Unmarshal(data, &historyFile); err != nil {
		t.Fatalf("Failed to parse history file: %s", err)
	}

	entry := historyFile.Entries[0]
	if entry.PlanSummary == nil {
		t.Fatal("PlanSummary should not be nil")
	}

	if entry.PlanSummary.Add != 5 {
		t.Errorf("Expected Add=5, got %d", entry.PlanSummary.Add)
	}
	if entry.PlanSummary.Change != 3 {
		t.Errorf("Expected Change=3, got %d", entry.PlanSummary.Change)
	}
	if entry.PlanSummary.Destroy != 2 {
		t.Errorf("Expected Destroy=2, got %d", entry.PlanSummary.Destroy)
	}
	if entry.PlanSummary.HasDrift {
		t.Error("Expected HasDrift=false")
	}
}

func TestHistoryHooks_UpdateStateInfo(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)
	hooks.manager.Enable()

	err := hooks.BeforeCommand("apply", []string{})
	if err != nil {
		t.Fatalf("BeforeCommand failed: %s", err)
	}

	// Update state info
	hooks.UpdateStateInfo("local", 4, 10, 15)

	err = hooks.AfterCommand(0)
	if err != nil {
		t.Fatalf("AfterCommand failed: %s", err)
	}

	// Verify state info was recorded
	historyPath := filepath.Join(tmpDir, history.HistoryFileName)
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("Failed to read history file: %s", err)
	}

	var historyFile history.HistoryFile
	if err := json.Unmarshal(data, &historyFile); err != nil {
		t.Fatalf("Failed to parse history file: %s", err)
	}

	entry := historyFile.Entries[0]
	if entry.StateInfo == nil {
		t.Fatal("StateInfo should not be nil")
	}

	if entry.StateInfo.BackendType != "local" {
		t.Errorf("Expected BackendType='local', got '%s'", entry.StateInfo.BackendType)
	}
	if entry.StateInfo.StateVersion != 4 {
		t.Errorf("Expected StateVersion=4, got %d", entry.StateInfo.StateVersion)
	}
	if entry.StateInfo.ResourcesBefore != 10 {
		t.Errorf("Expected ResourcesBefore=10, got %d", entry.StateInfo.ResourcesBefore)
	}
	if entry.StateInfo.ResourcesAfter != 15 {
		t.Errorf("Expected ResourcesAfter=15, got %d", entry.StateInfo.ResourcesAfter)
	}
}

func TestHistoryHooks_AddResourceChange(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)
	hooks.manager.Enable()

	err := hooks.BeforeCommand("apply", []string{})
	if err != nil {
		t.Fatalf("BeforeCommand failed: %s", err)
	}

	// Add resource changes
	hooks.AddResourceChange("aws_instance.example", "create", "aws", "aws_instance")
	hooks.AddResourceChange("aws_s3_bucket.data", "update", "aws", "aws_s3_bucket")

	err = hooks.AfterCommand(0)
	if err != nil {
		t.Fatalf("AfterCommand failed: %s", err)
	}

	// Verify resource changes were recorded
	historyPath := filepath.Join(tmpDir, history.HistoryFileName)
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("Failed to read history file: %s", err)
	}

	var historyFile history.HistoryFile
	if err := json.Unmarshal(data, &historyFile); err != nil {
		t.Fatalf("Failed to parse history file: %s", err)
	}

	entry := historyFile.Entries[0]
	if len(entry.ResourcesAffected) != 2 {
		t.Fatalf("Expected 2 resource changes, got %d", len(entry.ResourcesAffected))
	}

	// Check first resource
	if entry.ResourcesAffected[0].Address != "aws_instance.example" {
		t.Errorf("Expected address 'aws_instance.example', got '%s'", entry.ResourcesAffected[0].Address)
	}
	if entry.ResourcesAffected[0].Action != "create" {
		t.Errorf("Expected action 'create', got '%s'", entry.ResourcesAffected[0].Action)
	}

	// Check second resource
	if entry.ResourcesAffected[1].Address != "aws_s3_bucket.data" {
		t.Errorf("Expected address 'aws_s3_bucket.data', got '%s'", entry.ResourcesAffected[1].Address)
	}
	if entry.ResourcesAffected[1].Action != "update" {
		t.Errorf("Expected action 'update', got '%s'", entry.ResourcesAffected[1].Action)
	}
}

func TestHistoryHooks_OnError(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)
	hooks.manager.Enable()

	err := hooks.BeforeCommand("plan", []string{})
	if err != nil {
		t.Fatalf("BeforeCommand failed: %s", err)
	}

	// Simulate an error
	testErr := os.ErrNotExist
	hooks.OnError(testErr)

	// Finish with error exit code
	err = hooks.AfterCommand(1)
	if err != nil {
		t.Fatalf("AfterCommand failed: %s", err)
	}

	// Verify error was recorded
	historyPath := filepath.Join(tmpDir, history.HistoryFileName)
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("Failed to read history file: %s", err)
	}

	var historyFile history.HistoryFile
	if err := json.Unmarshal(data, &historyFile); err != nil {
		t.Fatalf("Failed to parse history file: %s", err)
	}

	entry := historyFile.Entries[0]
	if entry.ErrorDetails == nil {
		t.Fatal("ErrorDetails should not be nil")
	}

	if entry.ErrorDetails.Message != testErr.Error() {
		t.Errorf("Expected error message '%s', got '%s'", testErr.Error(), entry.ErrorDetails.Message)
	}

	if entry.ExitCode == nil || *entry.ExitCode != 1 {
		t.Errorf("Expected exit code 1, got %v", entry.ExitCode)
	}
}

func TestHistoryHooks_MultipleCommands(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)
	hooks.manager.Enable()

	// Record first command
	hooks.BeforeCommand("plan", []string{})
	hooks.AfterCommand(0)

	// Record second command
	hooks.BeforeCommand("apply", []string{"-auto-approve"})
	hooks.AfterCommand(0)

	// Record third command
	hooks.BeforeCommand("destroy", []string{"-auto-approve"})
	hooks.AfterCommand(0)

	// Verify all commands were recorded
	historyPath := filepath.Join(tmpDir, history.HistoryFileName)
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("Failed to read history file: %s", err)
	}

	var historyFile history.HistoryFile
	if err := json.Unmarshal(data, &historyFile); err != nil {
		t.Fatalf("Failed to parse history file: %s", err)
	}

	if len(historyFile.Entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(historyFile.Entries))
	}

	// Verify commands are in reverse chronological order (newest first)
	if historyFile.Entries[0].Command != "destroy" {
		t.Errorf("Expected first entry to be 'destroy', got '%s'", historyFile.Entries[0].Command)
	}
	if historyFile.Entries[1].Command != "apply" {
		t.Errorf("Expected second entry to be 'apply', got '%s'", historyFile.Entries[1].Command)
	}
	if historyFile.Entries[2].Command != "plan" {
		t.Errorf("Expected third entry to be 'plan', got '%s'", historyFile.Entries[2].Command)
	}
}

func TestGetHistoryWorkingDir(t *testing.T) {
	wd := GetHistoryWorkingDir()
	if wd == "" {
		t.Error("GetHistoryWorkingDir returned empty string")
	}

	// Should return current directory or "." on error
	if wd != "." {
		// Verify it's a valid directory
		if _, err := os.Stat(wd); os.IsNotExist(err) {
			t.Errorf("GetHistoryWorkingDir returned non-existent directory: %s", wd)
		}
	}
}

// Made with Bob
