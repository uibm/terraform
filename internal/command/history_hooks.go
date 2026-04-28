package command

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/terraform/internal/command/history"
	"github.com/hashicorp/terraform/internal/plans"
	"github.com/hashicorp/terraform/internal/states"
	"github.com/hashicorp/terraform/internal/tfdiags"
	"github.com/urfave/cli"
)

// HistoryRecorder handles automatic history recording for commands
type HistoryRecorder struct {
	manager *history.Manager
	entry   *history.Entry
}

// NewHistoryRecorder creates a new history recorder
func NewHistoryRecorder(workingDir string) *HistoryRecorder {
	return &HistoryRecorder{
		manager: history.NewManager(workingDir),
	}
}

// StartRecording begins recording a command execution
func (hr *HistoryRecorder) StartRecording(command string, args []string) error {
	if !hr.manager.IsEnabled() {
		return nil
	}

	entry, err := hr.manager.StartRecording(command, args)
	if err != nil {
		return err
	}

	hr.entry = entry
	return nil
}

// FinishRecording completes the recording with final results
func (hr *HistoryRecorder) FinishRecording(exitCode int, diags tfdiags.Diagnostics) error {
	if !hr.manager.IsEnabled() || hr.entry == nil {
		return nil
	}

	var errorDetails *history.ErrorDetails
	if diags.HasErrors() {
		errorDetails = &history.ErrorDetails{
			Message: diags.Err().Error(),
			Type:    "terraform_error",
		}

		// Extract detailed error information
		for _, diag := range diags {
			if diag.Severity() == tfdiags.Error {
				errorDetails.Diagnostic = diag.Description().Detail
				break
			}
		}
	}

	return hr.manager.FinishRecording(exitCode, errorDetails)
}

// RecordPlanSummary records plan execution summary
func (hr *HistoryRecorder) RecordPlanSummary(plan *plans.Plan) {
	if !hr.manager.IsEnabled() || hr.entry == nil || plan == nil {
		return
	}

	summary := &history.PlanSummary{
		Add:      0,
		Change:   0,
		Destroy:  0,
		HasDrift: false,
	}

	// Count changes by action
	for _, change := range plan.Changes.Resources {
		switch change.Action {
		case plans.Create:
			summary.Add++
		case plans.Update:
			summary.Change++
		case plans.Delete, plans.DeleteThenCreate, plans.CreateThenDelete:
			summary.Destroy++
		}
	}

	// Check for drift
	if len(plan.DriftedResources) > 0 {
		summary.HasDrift = true
	}

	hr.manager.UpdatePlanSummary(summary)

	// Record individual resource changes
	for _, change := range plan.Changes.Resources {
		resourceChange := history.ResourceChange{
			Address:      change.Addr.String(),
			Action:       change.Action.String(),
			Provider:     change.ProviderAddr.Provider.String(),
			ResourceType: change.Addr.Resource.Resource.Type,
		}

		// Add change reason if available
		if change.ActionReason != plans.ResourceInstanceChangeNoReason {
			resourceChange.ChangeReason = change.ActionReason.String()
		}

		hr.manager.AddResourceChange(resourceChange)
	}
}

// RecordStateInfo records state information before and after operations
func (hr *HistoryRecorder) RecordStateInfo(beforeState, afterState *states.State, backendType string) {
	if !hr.manager.IsEnabled() || hr.entry == nil {
		return
	}

	stateInfo := &history.StateInfo{
		BackendType: backendType,
	}

	if beforeState != nil {
		stateInfo.ResourcesBefore = len(beforeState.RootModule().Resources)
		stateInfo.StateVersion = int(beforeState.Serial)
	}

	if afterState != nil {
		stateInfo.ResourcesAfter = len(afterState.RootModule().Resources)
		if afterState.Serial > 0 {
			stateInfo.StateVersion = int(afterState.Serial)
		}
	}

	// Calculate changes
	if beforeState != nil && afterState != nil {
		beforeCount := len(beforeState.RootModule().Resources)
		afterCount := len(afterState.RootModule().Resources)

		if afterCount > beforeCount {
			stateInfo.ResourcesAdded = afterCount - beforeCount
		} else if afterCount < beforeCount {
			stateInfo.ResourcesDestroyed = beforeCount - afterCount
		}

		// Note: Calculating exact changed resources would require deeper analysis
		// For now, we'll rely on plan summary for accurate change counts
	}

	hr.manager.UpdateStateInfo(stateInfo)
}

// IsEnabled returns true if history recording is enabled
func (hr *HistoryRecorder) IsEnabled() bool {
	return hr.manager.IsEnabled()
}

// EnableHistory enables history tracking for the current directory
func EnableHistory(workingDir string) error {
	manager := history.NewManager(workingDir)
	manager.Enable()

	// Create initial history file if it doesn't exist
	historyPath := filepath.Join(workingDir, history.HistoryFileName)
	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		historyFile := &history.HistoryFile{
			Version:   "1.0",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Config:    *manager.Config,
			Entries:   []history.Entry{},
		}

		return writeHistoryFile(historyPath, historyFile)
	}

	return nil
}

// DisableHistory disables history tracking for the current directory
func DisableHistory(workingDir string) error {
	manager := history.NewManager(workingDir)
	manager.Disable()
	return nil
}

// IsHistoryEnabled checks if history is enabled for the current directory
func IsHistoryEnabled(workingDir string) bool {
	manager := history.NewManager(workingDir)
	return manager.IsEnabled()
}

// writeHistoryFile is a helper to write history file
func writeHistoryFile(path string, historyFile *history.HistoryFile) error {
	data, err := json.MarshalIndent(historyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history file: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

// Enhanced Meta to include history recording
type MetaWithHistory struct {
	Meta
	historyRecorder *HistoryRecorder
}

// NewMetaWithHistory creates a new Meta with history recording capabilities
func NewMetaWithHistory(original Meta) *MetaWithHistory {
	workingDir, _ := os.Getwd()
	return &MetaWithHistory{
		Meta:            original,
		historyRecorder: NewHistoryRecorder(workingDir),
	}
}

// StartHistoryRecording begins recording for a command
func (m *MetaWithHistory) StartHistoryRecording(command string, args []string) error {
	return m.historyRecorder.StartRecording(command, args)
}

// FinishHistoryRecording completes recording for a command
func (m *MetaWithHistory) FinishHistoryRecording(exitCode int, diags tfdiags.Diagnostics) error {
	return m.historyRecorder.FinishRecording(exitCode, diags)
}

// RecordPlan records plan information
func (m *MetaWithHistory) RecordPlan(plan *plans.Plan) {
	m.historyRecorder.RecordPlanSummary(plan)
}

// RecordState records state information
func (m *MetaWithHistory) RecordState(beforeState, afterState *states.State, backendType string) {
	m.historyRecorder.RecordStateInfo(beforeState, afterState, backendType)
}

// CommandWrapper wraps a command to automatically record history
type CommandWrapper struct {
	command cli.Command
	name    string
	args    []string
}

// NewCommandWrapper creates a new command wrapper
func NewCommandWrapper(command cli.Command, name string, args []string) *CommandWrapper {
	return &CommandWrapper{
		command: command,
		name:    name,
		args:    args,
	}
}

// Run executes the wrapped command with history recording
func (cw *CommandWrapper) Run(args []string) int {
	workingDir, err := os.Getwd()
	if err != nil {
		// If we can't get working directory, just run the command normally
		return cw.command.Run(args)
	}

	recorder := NewHistoryRecorder(workingDir)

	// Check if we should skip history for this command
	if shouldSkipHistory(cw.name) {
		return cw.command.Run(args)
	}

	// Start recording
	if err := recorder.StartRecording(cw.name, args); err != nil {
		// If recording fails, continue with command execution
		// We don't want history issues to break normal operations
		return cw.command.Run(args)
	}

	// Execute the command
	exitCode := cw.command.Run(args)

	// Finish recording
	var diags tfdiags.Diagnostics
	if exitCode != 0 {
		diags = diags.Append(fmt.Errorf("command failed with exit code %d", exitCode))
	}

	if err := recorder.FinishRecording(exitCode, diags); err != nil {
		// Log error but don't affect command exit code
		fmt.Fprintf(os.Stderr, "Warning: Failed to record command history: %v\n", err)
	}

	return exitCode
}

// Help delegates to the wrapped command
func (cw *CommandWrapper) Help() string {
	return cw.command.Help()
}

// Synopsis delegates to the wrapped command
func (cw *CommandWrapper) Synopsis() string {
	return cw.command.Synopsis()
}

// shouldSkipHistory determines if a command should be excluded from history
func shouldSkipHistory(command string) bool {
	skipCommands := map[string]bool{
		"history": true, // Avoid recursive history recording
		"version": true,
		"help":    true,
	}

	return skipCommands[command]
}

// Integration with existing command infrastructure

// Enhanced Apply Command with history integration
type ApplyCommandWithHistory struct {
	*ApplyCommand
	recorder *HistoryRecorder
}

// Run executes apply with history recording
func (c *ApplyCommandWithHistory) Run(args []string) int {
	workingDir, _ := os.Getwd()
	c.recorder = NewHistoryRecorder(workingDir)

	// Start recording
	if err := c.recorder.StartRecording("apply", args); err != nil {
		// Continue without history if recording fails
		return c.ApplyCommand.Run(args)
	}

	// Execute the original apply command with state tracking
	exitCode := c.runWithStateTracking(args)

	// Finish recording
	var diags tfdiags.Diagnostics
	if exitCode != 0 {
		diags = diags.Append(fmt.Errorf("apply failed with exit code %d", exitCode))
	}

	if err := c.recorder.FinishRecording(exitCode, diags); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to record apply history: %v\n", err)
	}

	return exitCode
}

// runWithStateTracking executes apply while tracking state changes
func (c *ApplyCommandWithHistory) runWithStateTracking(args []string) int {
	// Get initial state
	initialState, err := c.getState()
	if err == nil && c.recorder.IsEnabled() {
		// Record initial state info
		backendType := c.getBackendType()
		c.recorder.RecordStateInfo(initialState, nil, backendType)
	}

	// Execute the original apply
	exitCode := c.ApplyCommand.Run(args)

	// Get final state and record changes
	if exitCode == 0 && c.recorder.IsEnabled() {
		finalState, err := c.getState()
		if err == nil {
			backendType := c.getBackendType()
			c.recorder.RecordStateInfo(initialState, finalState, backendType)
		}
	}

	return exitCode
}

// getState retrieves the current Terraform state
func (c *ApplyCommandWithHistory) getState() (*states.State, error) {
	// This would integrate with Terraform's state management
	// Implementation would depend on internal Terraform APIs
	return nil, nil
}

// getBackendType returns the current backend type
func (c *ApplyCommandWithHistory) getBackendType() string {
	// This would integrate with Terraform's backend configuration
	// Implementation would depend on internal Terraform APIs
	return "unknown"
}

// Enhanced Plan Command with history integration
type PlanCommandWithHistory struct {
	*PlanCommand
	recorder *HistoryRecorder
}

// Run executes plan with history recording
func (c *PlanCommandWithHistory) Run(args []string) int {
	workingDir, _ := os.Getwd()
	c.recorder = NewHistoryRecorder(workingDir)

	// Start recording
	if err := c.recorder.StartRecording("plan", args); err != nil {
		return c.PlanCommand.Run(args)
	}

	// Execute plan and capture results
	exitCode := c.runWithPlanCapture(args)

	// Finish recording
	var diags tfdiags.Diagnostics
	if exitCode != 0 {
		diags = diags.Append(fmt.Errorf("plan failed with exit code %d", exitCode))
	}

	if err := c.recorder.FinishRecording(exitCode, diags); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to record plan history: %v\n", err)
	}

	return exitCode
}

// runWithPlanCapture executes plan while capturing plan details
func (c *PlanCommandWithHistory) runWithPlanCapture(args []string) int {
	// This would need to integrate with Terraform's plan generation
	// to capture the plan object and record it

	exitCode := c.PlanCommand.Run(args)

	// If successful, try to capture plan information
	if exitCode == 0 && c.recorder.IsEnabled() {
		// This would require access to the generated plan
		// plan := c.getGeneratedPlan()
		// if plan != nil {
		//     c.recorder.RecordPlanSummary(plan)
		// }
	}

	return exitCode
}

// Enhanced Destroy Command with history integration
type DestroyCommandWithHistory struct {
	*DestroyCommand
	recorder *HistoryRecorder
}

// Run executes destroy with history recording
func (c *DestroyCommandWithHistory) Run(args []string) int {
	workingDir, _ := os.Getwd()
	c.recorder = NewHistoryRecorder(workingDir)

	// Start recording
	if err := c.recorder.StartRecording("destroy", args); err != nil {
		return c.DestroyCommand.Run(args)
	}

	// Get initial state for tracking
	initialState, _ := c.getState()

	// Execute destroy
	exitCode := c.DestroyCommand.Run(args)

	// Record final state
	if c.recorder.IsEnabled() {
		finalState, _ := c.getState()
		backendType := c.getBackendType()
		c.recorder.RecordStateInfo(initialState, finalState, backendType)
	}

	// Finish recording
	var diags tfdiags.Diagnostics
	if exitCode != 0 {
		diags = diags.Append(fmt.Errorf("destroy failed with exit code %d", exitCode))
	}

	if err := c.recorder.FinishRecording(exitCode, diags); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to record destroy history: %v\n", err)
	}

	return exitCode
}

// getState and getBackendType methods for DestroyCommandWithHistory
func (c *DestroyCommandWithHistory) getState() (*states.State, error) {
	return nil, nil // Implementation depends on internal APIs
}

func (c *DestroyCommandWithHistory) getBackendType() string {
	return "unknown" // Implementation depends on internal APIs
}

// HistoryMiddleware provides middleware for command execution
type HistoryMiddleware struct {
	workingDir string
}

// NewHistoryMiddleware creates a new history middleware
func NewHistoryMiddleware(workingDir string) *HistoryMiddleware {
	return &HistoryMiddleware{
		workingDir: workingDir,
	}
}

// WrapCommand wraps a command with history recording
func (hm *HistoryMiddleware) WrapCommand(commandName string, command cli.Command) cli.Command {
	if shouldSkipHistory(commandName) {
		return command
	}

	return &CommandWrapper{
		command: command,
		name:    commandName,
	}
}

// Auto-detection and configuration helpers

// DetectAndConfigureHistory automatically detects if history should be enabled
func DetectAndConfigureHistory(workingDir string) error {
	// Check if this is a Terraform project
	if !isTerraformProject(workingDir) {
		return nil
	}

	// Check if history is already configured
	historyPath := filepath.Join(workingDir, history.HistoryFileName)
	if _, err := os.Stat(historyPath); err == nil {
		return nil // Already configured
	}

	// Check for environment variable to auto-enable
	if os.Getenv("TF_ENABLE_HISTORY") == "true" {
		return EnableHistory(workingDir)
	}

	// Check for .terraform-history file as a marker
	markerPath := filepath.Join(workingDir, ".terraform-history")
	if _, err := os.Stat(markerPath); err == nil {
		return EnableHistory(workingDir)
	}

	return nil
}

// isTerraformProject checks if the directory contains Terraform files
func isTerraformProject(workingDir string) bool {
	// Check for .tf files
	matches, err := filepath.Glob(filepath.Join(workingDir, "*.tf"))
	if err == nil && len(matches) > 0 {
		return true
	}

	// Check for .terraform directory
	terraformDir := filepath.Join(workingDir, ".terraform")
	if _, err := os.Stat(terraformDir); err == nil {
		return true
	}

	return false
}

// LoadHistoryConfig loads history configuration from file
func LoadHistoryConfig(workingDir string) (*history.Config, error) {
	configPath := filepath.Join(workingDir, ".terraform-history.json")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Return default config if no file exists
		return &history.Config{
			Enabled:          false,
			MaxEntries:       history.DefaultMaxEntries,
			RetentionDays:    history.DefaultRetentionDays,
			IncludeSensitive: false,
		}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read history config: %w", err)
	}

	var config history.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse history config: %w", err)
	}

	return &config, nil
}

// SaveHistoryConfig saves history configuration to file
func SaveHistoryConfig(workingDir string, config *history.Config) error {
	configPath := filepath.Join(workingDir, ".terraform-history.json")

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history config: %w", err)
	}

	return os.WriteFile(configPath, data, 0600)
}
