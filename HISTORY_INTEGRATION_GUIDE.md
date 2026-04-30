# History Feature Integration Guide

**Status:** Infrastructure Complete, Integration Pending  
**Branch:** history-support  
**Last Updated:** 2026-04-30

---

## Overview

The history feature is **95% complete** but **not yet functional** because it lacks integration with Terraform's command execution flow. This guide provides step-by-step instructions to complete the integration.

## Current Status

### ✅ Completed Components

1. **History Manager** (`internal/command/history/manager.go`)
   - Entry creation and recording
   - File locking for concurrent access
   - Retention policies
   - Git integration
   - Environment capture

2. **History Command** (`internal/command/history.go`)
   - List, show, export, clean subcommands
   - Multiple output formats (table, JSON, CSV)
   - Filtering and search
   - Data anonymization

3. **History Hooks** (`internal/command/history_hooks.go`)
   - BeforeCommand/AfterCommand hooks
   - Plan summary updates
   - State info updates
   - Resource change tracking
   - Error handling

4. **Command Registration** (`commands.go`)
   - History command registered and accessible

### ❌ Missing Integration

1. **No hooks in main.go** - History never starts recording
2. **No command-specific integration** - Plan/apply/destroy don't update history
3. **No state manager integration** - State changes not captured
4. **No tests** - Zero test coverage

---

## Integration Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         main.go                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ 1. Create HistoryHooks                                 │ │
│  │ 2. Call BeforeCommand(cmd, args)                       │ │
│  │ 3. Execute command via cliRunner.Run()                 │ │
│  │ 4. Call AfterCommand(exitCode)                         │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                   Individual Commands                        │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ PlanCommand:                                           │ │
│  │   - Call hooks.UpdatePlanSummary(add, change, destroy) │ │
│  │                                                        │ │
│  │ ApplyCommand:                                          │ │
│  │   - Call hooks.UpdateStateInfo(...)                   │ │
│  │   - Call hooks.AddResourceChange(...)                 │ │
│  │                                                        │ │
│  │ DestroyCommand:                                        │ │
│  │   - Call hooks.AddResourceChange(...)                 │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    History Manager                           │
│  - Records entries to terraform.history file                 │
│  - Applies retention policies                                │
│  - Manages file locking                                      │
└─────────────────────────────────────────────────────────────┘
```

---

## Step-by-Step Integration

### Step 1: Modify main.go

**Location:** `main.go`, around line 249 (after `initCommands()`)

**Add history initialization:**

```go
// After line 249: initCommands(ctx, originalWd, streams, config, services, providerSrc, providerDevOverrides, unmanagedProviders)

// Initialize history tracking
var historyHooks *command.HistoryHooks
workingDir, err := os.Getwd()
if err == nil {
	historyHooks = command.NewHistoryHooks(workingDir)
}
```

**Location:** `main.go`, around line 320 (before command execution)

**Add BeforeCommand hook:**

```go
// After line 320: if cmd := cliRunner.Subcommand(); cmd != "" && !autoComplete {
// ... existing code ...

// Start history recording if enabled
if historyHooks != nil && historyHooks.IsEnabled() {
	if err := historyHooks.BeforeCommand(cliRunner.Subcommand(), args); err != nil {
		log.Printf("[WARN] Failed to start history recording: %s", err)
	}
}
```

**Location:** `main.go`, around line 343 (after command execution)

**Add AfterCommand hook:**

```go
// Replace line 339-343:
exitCode, err := cliRunner.Run()
if err != nil {
	Ui.Error(fmt.Sprintf("Error executing CLI: %s", err.Error()))
	return 1
}

// With:
exitCode, err := cliRunner.Run()
if err != nil {
	Ui.Error(fmt.Sprintf("Error executing CLI: %s", err.Error()))
	if historyHooks != nil {
		historyHooks.OnError(err)
	}
	exitCode = 1
}

// Finish history recording
if historyHooks != nil && historyHooks.IsEnabled() {
	if err := historyHooks.AfterCommand(exitCode); err != nil {
		log.Printf("[WARN] Failed to finish history recording: %s", err)
	}
}
```

### Step 2: Integrate with Plan Command

**Location:** `internal/command/plan.go`

**Find the section where plan results are displayed** (search for "Plan:" or plan summary output)

**Add after plan calculation:**

```go
// After plan is calculated and before displaying results
if c.Meta.historyHooks != nil && c.Meta.historyHooks.IsEnabled() {
	add := 0
	change := 0
	destroy := 0
	hasDrift := false
	
	// Extract counts from plan
	for _, change := range plan.Changes.Resources {
		switch change.Action {
		case plans.Create:
			add++
		case plans.Update:
			change++
		case plans.Delete, plans.DeleteThenCreate, plans.CreateThenDelete:
			destroy++
		}
	}
	
	// Check for drift
	if len(plan.DriftedResources) > 0 {
		hasDrift = true
	}
	
	c.Meta.historyHooks.UpdatePlanSummary(add, change, destroy, hasDrift)
}
```

### Step 3: Integrate with Apply Command

**Location:** `internal/command/apply.go`

**Add after successful apply:**

```go
// After apply completes successfully
if c.Meta.historyHooks != nil && c.Meta.historyHooks.IsEnabled() {
	// Update state info
	if newState != nil {
		resourceCount := len(newState.Resources())
		c.Meta.historyHooks.UpdateStateInfo(
			c.Meta.backendType,
			newState.Version,
			oldResourceCount, // You'll need to capture this before apply
			resourceCount,
		)
	}
	
	// Add resource changes
	for _, change := range plan.Changes.Resources {
		action := "no-op"
		switch change.Action {
		case plans.Create:
			action = "create"
		case plans.Update:
			action = "update"
		case plans.Delete:
			action = "delete"
		}
		
		c.Meta.historyHooks.AddResourceChange(
			change.Addr.String(),
			action,
			change.ProviderAddr.Provider.String(),
			change.Addr.Resource.Type,
		)
	}
}
```

### Step 4: Integrate with Destroy Command

**Location:** `internal/command/destroy.go`

**Similar to apply, add after successful destroy:**

```go
// After destroy completes successfully
if c.Meta.historyHooks != nil && c.Meta.historyHooks.IsEnabled() {
	for _, change := range plan.Changes.Resources {
		c.Meta.historyHooks.AddResourceChange(
			change.Addr.String(),
			"delete",
			change.ProviderAddr.Provider.String(),
			change.Addr.Resource.Type,
		)
	}
}
```

### Step 5: Add HistoryHooks to Meta

**Location:** `internal/command/meta.go`

**Add field to Meta struct:**

```go
type Meta struct {
	// ... existing fields ...
	
	// History tracking hooks
	historyHooks *HistoryHooks
}
```

**Initialize in NewMeta or similar:**

```go
func (m *Meta) SetHistoryHooks(hooks *HistoryHooks) {
	m.historyHooks = hooks
}
```

**Pass hooks from main.go to commands:**

```go
// In initCommands(), after creating meta:
if historyHooks != nil {
	meta.SetHistoryHooks(historyHooks)
}
```

---

## Testing Strategy

### Unit Tests

**File:** `internal/command/history_test.go`

```go
package command

import (
	"os"
	"path/filepath"
	"testing"
	
	"github.com/hashicorp/terraform/internal/command/history"
)

func TestHistoryHooks_BeforeAfterCommand(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)
	
	// Enable history
	hooks.manager.Enable()
	
	// Test recording
	err := hooks.BeforeCommand("plan", []string{})
	if err != nil {
		t.Fatalf("BeforeCommand failed: %s", err)
	}
	
	err = hooks.AfterCommand(0)
	if err != nil {
		t.Fatalf("AfterCommand failed: %s", err)
	}
	
	// Verify history file was created
	historyPath := filepath.Join(tmpDir, history.HistoryFileName)
	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		t.Fatal("History file was not created")
	}
}

func TestHistoryHooks_UpdatePlanSummary(t *testing.T) {
	tmpDir := t.TempDir()
	hooks := NewHistoryHooks(tmpDir)
	hooks.manager.Enable()
	
	hooks.BeforeCommand("plan", []string{})
	hooks.UpdatePlanSummary(5, 3, 2, false)
	hooks.AfterCommand(0)
	
	// Verify plan summary was recorded
	// (Add verification logic)
}
```

### Integration Tests

**File:** `internal/command/history_integration_test.go`

```go
package command

import (
	"testing"
)

func TestHistoryIntegration_PlanCommand(t *testing.T) {
	// Create test fixture with terraform config
	// Run plan command
	// Verify history was recorded with plan summary
}

func TestHistoryIntegration_ApplyCommand(t *testing.T) {
	// Create test fixture
	// Run apply command
	// Verify history was recorded with state changes
}
```

---

## Configuration

### Enable History Tracking

**Option 1: Via Command**
```bash
terraform history enable
```

**Option 2: Via Environment Variable**
```bash
export TF_HISTORY_ENABLED=true
```

**Option 3: Via Config File**
Create `.terraform/history.config`:
```json
{
  "enabled": true,
  "max_entries": 1000,
  "retention_days": 90,
  "include_sensitive": false
}
```

---

## Verification

### Test the Integration

1. **Enable history:**
   ```bash
   terraform history enable
   ```

2. **Run a plan:**
   ```bash
   terraform plan
   ```

3. **Check history:**
   ```bash
   terraform history list
   ```

4. **Expected output:**
   ```
   ID                   DATE                COMMAND    WORKSPACE  EXIT  DURATION  CHANGES
   ------------------------------------------------------------------------------------
   tf-hist-abc123...    2026-04-30 18:30:00 plan       default    0     2.3s      +5 ~3 -2
   ```

### Verify History File

```bash
cat terraform.history | jq '.'
```

Expected structure:
```json
{
  "version": "1.0",
  "created_at": "2026-04-30T18:30:00Z",
  "updated_at": "2026-04-30T18:30:00Z",
  "config": {
    "enabled": true,
    "max_entries": 1000,
    "retention_days": 90,
    "include_sensitive": false
  },
  "entries": [
    {
      "id": "tf-hist-abc123...",
      "timestamp": "2026-04-30T18:30:00Z",
      "command": "plan",
      "arguments": [],
      "working_directory": "/path/to/project",
      "workspace": "default",
      "terraform_version": "1.x.x",
      "exit_code": 0,
      "execution_time": 2.3,
      "user": "username",
      "git_info": {
        "branch": "main",
        "commit": "abc123",
        "is_dirty": false
      },
      "plan_summary": {
        "add": 5,
        "change": 3,
        "destroy": 2,
        "has_drift": false
      }
    }
  ]
}
```

---

## Troubleshooting

### History Not Recording

1. **Check if enabled:**
   ```bash
   terraform history list
   # If empty, enable it:
   terraform history enable
   ```

2. **Check file permissions:**
   ```bash
   ls -la terraform.history
   # Should be readable/writable by current user
   ```

3. **Check logs:**
   ```bash
   TF_LOG=DEBUG terraform plan 2>&1 | grep -i history
   ```

### Performance Issues

1. **Reduce retention:**
   ```bash
   terraform history clean --older-than 30d
   ```

2. **Disable for specific commands:**
   ```bash
   TF_HISTORY_ENABLED=false terraform plan
   ```

---

## Future Enhancements

### Phase 2 (After Basic Integration)

1. **Remote History Sync**
   - Store history in remote backend
   - Share history across team

2. **History Visualization**
   - Timeline view
   - Dependency graphs
   - Change patterns

3. **Advanced Analytics**
   - Command frequency
   - Error rates
   - Performance metrics

### Phase 3 (Advanced Features)

1. **History Replay**
   - Reproduce historical commands
   - Dry-run mode

2. **Alerts & Notifications**
   - Slack/email on failures
   - Webhook support

3. **Compliance & Audit**
   - Audit trail export
   - Compliance reporting
   - Change approval workflow

---

## References

- Feature Gap Analysis: `FEATURE_GAP_ANALYSIS.md`
- Branch Analysis: `BRANCH_ANALYSIS.md`
- History Manager: `internal/command/history/manager.go`
- History Command: `internal/command/history.go`
- History Hooks: `internal/command/history_hooks.go`

---

## Estimated Effort

| Task | Effort | Priority |
|------|--------|----------|
| Modify main.go | 2-3 hours | Critical |
| Integrate with Plan | 1-2 hours | High |
| Integrate with Apply | 2-3 hours | High |
| Integrate with Destroy | 1 hour | High |
| Add to Meta struct | 1 hour | High |
| Unit tests | 3-4 hours | High |
| Integration tests | 4-5 hours | Medium |
| Documentation | 2 hours | Medium |
| **Total** | **16-21 hours** | |

---

## Next Steps

1. ✅ Review this integration guide
2. ⬜ Implement Step 1 (main.go modifications)
3. ⬜ Test basic recording with simple command
4. ⬜ Implement Step 2 (Plan integration)
5. ⬜ Implement Step 3 (Apply integration)
6. ⬜ Implement Step 4 (Destroy integration)
7. ⬜ Implement Step 5 (Meta struct changes)
8. ⬜ Write unit tests
9. ⬜ Write integration tests
10. ⬜ Update documentation
11. ⬜ Submit for review

---

**End of Integration Guide**