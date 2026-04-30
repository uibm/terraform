// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package command

import (
	"os"

	"github.com/hashicorp/terraform/internal/command/history"
)

// HistoryHooks provides integration points for history tracking
type HistoryHooks struct {
	manager      *history.Manager
	currentEntry *history.Entry
}

// NewHistoryHooks creates a new history hooks instance
func NewHistoryHooks(workingDir string) *HistoryHooks {
	return &HistoryHooks{
		manager: history.NewManager(workingDir),
	}
}

// BeforeCommand is called before a command executes
func (h *HistoryHooks) BeforeCommand(command string, args []string) error {
	if !h.manager.IsEnabled() {
		return nil
	}

	entry, err := h.manager.StartRecording(command, args)
	if err != nil {
		// Log error but don't fail the command
		return nil
	}

	h.currentEntry = entry
	return nil
}

// AfterCommand is called after a command completes
func (h *HistoryHooks) AfterCommand(exitCode int) error {
	if !h.manager.IsEnabled() || h.currentEntry == nil {
		return nil
	}

	return h.manager.FinishRecording(exitCode, nil)
}

// OnError is called when a command encounters an error
func (h *HistoryHooks) OnError(err error) {
	if !h.manager.IsEnabled() || h.currentEntry == nil {
		return
	}

	errorDetails := &history.ErrorDetails{
		Message: err.Error(),
		Type:    "command_error",
	}

	// Store error details for later recording
	if h.currentEntry != nil {
		h.currentEntry.ErrorDetails = errorDetails
	}
}

// UpdatePlanSummary updates plan information for the current command
func (h *HistoryHooks) UpdatePlanSummary(add, change, destroy int, hasDrift bool) {
	if !h.manager.IsEnabled() {
		return
	}

	h.manager.UpdatePlanSummary(&history.PlanSummary{
		Add:      add,
		Change:   change,
		Destroy:  destroy,
		HasDrift: hasDrift,
	})
}

// UpdateStateInfo updates state information for the current command
func (h *HistoryHooks) UpdateStateInfo(backendType string, stateVersion, resourcesBefore, resourcesAfter int) {
	if !h.manager.IsEnabled() {
		return
	}

	h.manager.UpdateStateInfo(&history.StateInfo{
		BackendType:     backendType,
		StateVersion:    stateVersion,
		ResourcesBefore: resourcesBefore,
		ResourcesAfter:  resourcesAfter,
	})
}

// AddResourceChange adds a resource change to the current command
func (h *HistoryHooks) AddResourceChange(address, action, provider, resourceType string) {
	if !h.manager.IsEnabled() {
		return
	}

	h.manager.AddResourceChange(history.ResourceChange{
		Address:      address,
		Action:       action,
		Provider:     provider,
		ResourceType: resourceType,
	})
}

// IsEnabled returns whether history tracking is enabled
func (h *HistoryHooks) IsEnabled() bool {
	return h.manager.IsEnabled()
}

// GetWorkingDir returns the working directory for history
func GetHistoryWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// Made with Bob
