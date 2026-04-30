package command

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform/internal/command/history"
	"github.com/posener/complete"
)

// HistoryCommand implements the "terraform history" command
type HistoryCommand struct {
	Meta
}

// Run executes the history command
func (c *HistoryCommand) Run(args []string) int {
	if len(args) == 0 {
		c.Ui.Error("Usage: terraform history <subcommand> [options]")
		c.Ui.Error("")
		c.Ui.Error("Available subcommands:")
		c.Ui.Error("  list     List command history")
		c.Ui.Error("  show     Show detailed information about a specific command")
		c.Ui.Error("  export   Export history to file")
		c.Ui.Error("  clean    Clean old history entries")
		c.Ui.Error("  enable   Enable history tracking")
		c.Ui.Error("  disable  Disable history tracking")
		return 1
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "list":
		return c.runList(subArgs)
	case "show":
		return c.runShow(subArgs)
	case "export":
		return c.runExport(subArgs)
	case "clean":
		return c.runClean(subArgs)
	case "enable":
		return c.runEnable(subArgs)
	case "disable":
		return c.runDisable(subArgs)
	default:
		c.Ui.Error(fmt.Sprintf("Unknown subcommand: %s", subcommand))
		return 1
	}
}

// runList implements the "list" subcommand
func (c *HistoryCommand) runList(args []string) int {
	var (
		commandFilter   string
		workspaceFilter string
		since           string
		until           string
		limit           int
		exitCode        int
		output          string
		showErrors      bool
	)

	// Parse flags
	cmdFlags := c.Meta.extendedFlagSet("history list")
	cmdFlags.StringVar(&commandFilter, "command", "", "Filter by command type")
	cmdFlags.StringVar(&workspaceFilter, "workspace", "", "Filter by workspace")
	cmdFlags.StringVar(&since, "since", "", "Show entries since date (YYYY-MM-DD)")
	cmdFlags.StringVar(&until, "until", "", "Show entries until date (YYYY-MM-DD)")
	cmdFlags.IntVar(&limit, "limit", 50, "Maximum number of entries to show")
	cmdFlags.IntVar(&exitCode, "exit-code", -1, "Filter by exit code")
	cmdFlags.StringVar(&output, "output", "table", "Output format (table, json, csv)")
	cmdFlags.BoolVar(&showErrors, "errors-only", false, "Show only failed commands")

	if err := cmdFlags.Parse(args); err != nil {
		c.Ui.Error(fmt.Sprintf("Error parsing flags: %s", err))
		return 1
	}

	// Get current working directory
	workingDir, err := os.Getwd()
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error getting working directory: %s", err))
		return 1
	}

	// Load history
	entries, err := c.loadHistoryEntries(workingDir)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error loading history: %s", err))
		return 1
	}

	// Apply filters
	filteredEntries := c.filterEntries(entries, filterOptions{
		command:    commandFilter,
		workspace:  workspaceFilter,
		since:      since,
		until:      until,
		exitCode:   exitCode,
		showErrors: showErrors,
	})

	// Apply limit
	if limit > 0 && len(filteredEntries) > limit {
		filteredEntries = filteredEntries[:limit]
	}

	// Output results
	switch output {
	case "json":
		return c.outputJSON(filteredEntries)
	case "csv":
		return c.outputCSV(filteredEntries)
	default:
		return c.outputTable(filteredEntries)
	}
}

// runEnable implements the "enable" subcommand
func (c *HistoryCommand) runEnable(args []string) int {
	workingDir, err := os.Getwd()
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error getting working directory: %s", err))
		return 1
	}

	manager := history.NewManager(workingDir)
	manager.Enable()

	// Save configuration
	if err := c.saveHistoryConfig(workingDir, manager); err != nil {
		c.Ui.Error(fmt.Sprintf("Error saving configuration: %s", err))
		return 1
	}

	c.Ui.Output("History tracking enabled.")
	return 0
}

// runDisable implements the "disable" subcommand
func (c *HistoryCommand) runDisable(args []string) int {
	workingDir, err := os.Getwd()
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error getting working directory: %s", err))
		return 1
	}

	manager := history.NewManager(workingDir)
	manager.Disable()

	// Save configuration
	if err := c.saveHistoryConfig(workingDir, manager); err != nil {
		c.Ui.Error(fmt.Sprintf("Error saving configuration: %s", err))
		return 1
	}

	c.Ui.Output("History tracking disabled.")
	return 0
}

// filterOptions contains filtering options for history entries
type filterOptions struct {
	command    string
	workspace  string
	since      string
	until      string
	exitCode   int
	showErrors bool
}

// loadHistoryEntries loads history entries from the history file
func (c *HistoryCommand) loadHistoryEntries(workingDir string) ([]history.Entry, error) {
	historyPath := filepath.Join(workingDir, history.HistoryFileName)

	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		return []history.Entry{}, nil
	}

	data, err := os.ReadFile(historyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read history file: %w", err)
	}

	var historyFile history.HistoryFile
	if err := json.Unmarshal(data, &historyFile); err != nil {
		return nil, fmt.Errorf("failed to parse history file: %w", err)
	}

	// Sort by timestamp (newest first)
	sort.Slice(historyFile.Entries, func(i, j int) bool {
		return historyFile.Entries[i].Timestamp.After(historyFile.Entries[j].Timestamp)
	})

	return historyFile.Entries, nil
}

// filterEntries applies filters to history entries
func (c *HistoryCommand) filterEntries(entries []history.Entry, opts filterOptions) []history.Entry {
	var filtered []history.Entry

	for _, entry := range entries {
		// Command filter
		if opts.command != "" && entry.Command != opts.command {
			continue
		}

		// Workspace filter
		if opts.workspace != "" && entry.Workspace != opts.workspace {
			continue
		}

		// Exit code filter
		if opts.exitCode >= 0 && (entry.ExitCode == nil || *entry.ExitCode != opts.exitCode) {
			continue
		}

		// Errors only filter
		if opts.showErrors && (entry.ExitCode == nil || *entry.ExitCode == 0) {
			continue
		}

		// Date filters
		if opts.since != "" {
			sinceDate, err := time.Parse("2006-01-02", opts.since)
			if err == nil && entry.Timestamp.Before(sinceDate) {
				continue
			}
		}

		if opts.until != "" {
			untilDate, err := time.Parse("2006-01-02", opts.until)
			if err == nil && entry.Timestamp.After(untilDate.AddDate(0, 0, 1)) {
				continue
			}
		}

		filtered = append(filtered, entry)
	}

	return filtered
}

// outputTable outputs entries in table format
func (c *HistoryCommand) outputTable(entries []history.Entry) int {
	if len(entries) == 0 {
		c.Ui.Output("No history entries found.")
		return 0
	}

	// Table header
	c.Ui.Output(fmt.Sprintf("%-20s %-19s %-10s %-12s %-5s %-8s %-10s",
		"ID", "DATE", "COMMAND", "WORKSPACE", "EXIT", "DURATION", "CHANGES"))
	c.Ui.Output(strings.Repeat("-", 90))

	// Table rows
	for _, entry := range entries {
		id := entry.ID
		if len(id) > 20 {
			id = id[:17] + "..."
		}

		date := entry.Timestamp.Format("2006-01-02 15:04:05")
		command := entry.Command
		workspace := entry.Workspace
		if workspace == "" {
			workspace = "default"
		}

		exitCode := "?"
		if entry.ExitCode != nil {
			exitCode = strconv.Itoa(*entry.ExitCode)
		}

		duration := "?"
		if entry.ExecutionTime != nil {
			duration = fmt.Sprintf("%.1fs", *entry.ExecutionTime)
		}

		changes := ""
		if entry.PlanSummary != nil {
			changes = fmt.Sprintf("+%d ~%d -%d",
				entry.PlanSummary.Add,
				entry.PlanSummary.Change,
				entry.PlanSummary.Destroy)
		} else if entry.ExitCode != nil && *entry.ExitCode != 0 {
			changes = "Error"
		}

		c.Ui.Output(fmt.Sprintf("%-20s %-19s %-10s %-12s %-5s %-8s %-10s",
			id, date, command, workspace, exitCode, duration, changes))
	}

	return 0
}

// outputJSON outputs entries in JSON format
func (c *HistoryCommand) outputJSON(entries []history.Entry) int {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error marshaling JSON: %s", err))
		return 1
	}

	c.Ui.Output(string(data))
	return 0
}

// outputCSV outputs entries in CSV format
func (c *HistoryCommand) outputCSV(entries []history.Entry) int {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	// Write header
	header := []string{"ID", "Timestamp", "Command", "Workspace", "ExitCode", "Duration", "User", "GitBranch", "GitCommit"}
	if err := writer.Write(header); err != nil {
		c.Ui.Error(fmt.Sprintf("Error writing CSV header: %s", err))
		return 1
	}

	// Write rows
	for _, entry := range entries {
		exitCode := ""
		if entry.ExitCode != nil {
			exitCode = strconv.Itoa(*entry.ExitCode)
		}

		duration := ""
		if entry.ExecutionTime != nil {
			duration = fmt.Sprintf("%.2f", *entry.ExecutionTime)
		}

		gitBranch := ""
		gitCommit := ""
		if entry.GitInfo != nil {
			gitBranch = entry.GitInfo.Branch
			gitCommit = entry.GitInfo.Commit
		}

		row := []string{
			entry.ID,
			entry.Timestamp.Format(time.RFC3339),
			entry.Command,
			entry.Workspace,
			exitCode,
			duration,
			entry.User,
			gitBranch,
			gitCommit,
		}

		if err := writer.Write(row); err != nil {
			c.Ui.Error(fmt.Sprintf("Error writing CSV row: %s", err))
			return 1
		}
	}

	return 0
}

// showEntry displays detailed information about a single entry
func (c *HistoryCommand) showEntry(entry *history.Entry, verbose bool, section string) {
	c.Ui.Output(fmt.Sprintf("Command ID: %s", entry.ID))
	c.Ui.Output(fmt.Sprintf("Timestamp: %s", entry.Timestamp.Format("2006-01-02 15:04:05 UTC")))
	c.Ui.Output(fmt.Sprintf("Command: terraform %s %s", entry.Command, strings.Join(entry.SanitizedArgs, " ")))
	c.Ui.Output(fmt.Sprintf("Working Directory: %s", entry.WorkingDirectory))
	c.Ui.Output(fmt.Sprintf("Workspace: %s", entry.Workspace))
	c.Ui.Output(fmt.Sprintf("Terraform Version: %s", entry.TerraformVersion))

	if entry.ExitCode != nil {
		status := "Success"
		if *entry.ExitCode != 0 {
			status = "Failed"
		}
		c.Ui.Output(fmt.Sprintf("Exit Code: %d (%s)", *entry.ExitCode, status))
	}

	if entry.ExecutionTime != nil {
		c.Ui.Output(fmt.Sprintf("Execution Time: %.2f seconds", *entry.ExecutionTime))
	}

	c.Ui.Output(fmt.Sprintf("User: %s", entry.User))
	c.Ui.Output("")

	// Git Information
	if entry.GitInfo != nil && (section == "" || section == "git") {
		c.Ui.Output("Git Information:")
		c.Ui.Output(fmt.Sprintf("  Branch: %s", entry.GitInfo.Branch))
		c.Ui.Output(fmt.Sprintf("  Commit: %s", entry.GitInfo.Commit))
		c.Ui.Output(fmt.Sprintf("  Status: %s", map[bool]string{true: "Dirty", false: "Clean"}[entry.GitInfo.IsDirty]))
		if entry.GitInfo.RemoteURL != "" {
			c.Ui.Output(fmt.Sprintf("  Remote: %s", entry.GitInfo.RemoteURL))
		}
		c.Ui.Output("")
	}

	// State Information
	if entry.StateInfo != nil && (section == "" || section == "state-changes") {
		c.Ui.Output("State Information:")
		if entry.StateInfo.BackendType != "" {
			c.Ui.Output(fmt.Sprintf("  Backend: %s", entry.StateInfo.BackendType))
		}
		if entry.StateInfo.StateVersion > 0 {
			c.Ui.Output(fmt.Sprintf("  Version: %d", entry.StateInfo.StateVersion))
		}
		c.Ui.Output(fmt.Sprintf("  Resources: %d → %d", entry.StateInfo.ResourcesBefore, entry.StateInfo.ResourcesAfter))
		c.Ui.Output("")
	}

	// Plan Summary
	if entry.PlanSummary != nil && (section == "" || section == "plan") {
		c.Ui.Output("Plan Summary:")
		c.Ui.Output(fmt.Sprintf("  Add: %d resources", entry.PlanSummary.Add))
		c.Ui.Output(fmt.Sprintf("  Change: %d resources", entry.PlanSummary.Change))
		c.Ui.Output(fmt.Sprintf("  Destroy: %d resources", entry.PlanSummary.Destroy))
		c.Ui.Output(fmt.Sprintf("  Drift Detected: %s", map[bool]string{true: "Yes", false: "No"}[entry.PlanSummary.HasDrift]))
		c.Ui.Output("")
	}

	// Resources Affected
	if len(entry.ResourcesAffected) > 0 && (section == "" || section == "resources") {
		c.Ui.Output("Resources Affected:")
		for _, resource := range entry.ResourcesAffected {
			actionSymbol := map[string]string{
				"create": "+",
				"update": "~",
				"delete": "-",
				"read":   " ",
				"no-op":  " ",
			}[resource.Action]

			c.Ui.Output(fmt.Sprintf("  %s %s", actionSymbol, resource.Address))
			if verbose && resource.ChangeReason != "" {
				c.Ui.Output(fmt.Sprintf("    %s", resource.ChangeReason))
			}
		}
		c.Ui.Output("")
	}

	// Error Details
	if entry.ErrorDetails != nil {
		c.Ui.Output("Error Details:")
		c.Ui.Output(fmt.Sprintf("  Type: %s", entry.ErrorDetails.Type))
		c.Ui.Output(fmt.Sprintf("  Message: %s", entry.ErrorDetails.Message))
		if entry.ErrorDetails.Code != "" {
			c.Ui.Output(fmt.Sprintf("  Code: %s", entry.ErrorDetails.Code))
		}
		c.Ui.Output("")
	}

	// Environment (verbose mode only)
	if verbose && len(entry.Environment) > 0 {
		c.Ui.Output("Environment Variables:")
		for key, value := range entry.Environment {
			c.Ui.Output(fmt.Sprintf("  %s=%s", key, value))
		}
		c.Ui.Output("")
	}
}

// exportEntry exports a single entry to a file
func (c *HistoryCommand) exportEntry(entry *history.Entry, filename string) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	return os.WriteFile(filename, data, 0644)
}

// exportEntries exports multiple entries to a file
func (c *HistoryCommand) exportEntries(entries []history.Entry, filename, format string) error {
	switch format {
	case "json":
		data, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal entries: %w", err)
		}
		return os.WriteFile(filename, data, 0644)

	case "csv":
		file, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		defer file.Close()

		writer := csv.NewWriter(file)
		defer writer.Flush()

		// Write header
		header := []string{"ID", "Timestamp", "Command", "Arguments", "Workspace", "ExitCode", "Duration", "User", "GitBranch", "GitCommit"}
		if err := writer.Write(header); err != nil {
			return fmt.Errorf("failed to write CSV header: %w", err)
		}

		// Write rows
		for _, entry := range entries {
			exitCode := ""
			if entry.ExitCode != nil {
				exitCode = strconv.Itoa(*entry.ExitCode)
			}

			duration := ""
			if entry.ExecutionTime != nil {
				duration = fmt.Sprintf("%.2f", *entry.ExecutionTime)
			}

			gitBranch := ""
			gitCommit := ""
			if entry.GitInfo != nil {
				gitBranch = entry.GitInfo.Branch
				gitCommit = entry.GitInfo.Commit
			}

			row := []string{
				entry.ID,
				entry.Timestamp.Format(time.RFC3339),
				entry.Command,
				strings.Join(entry.SanitizedArgs, " "),
				entry.Workspace,
				exitCode,
				duration,
				entry.User,
				gitBranch,
				gitCommit,
			}

			if err := writer.Write(row); err != nil {
				return fmt.Errorf("failed to write CSV row: %w", err)
			}
		}

		return nil

	default:
		return fmt.Errorf("unsupported export format: %s", format)
	}
}

// anonymizeEntries removes sensitive information from entries
func (c *HistoryCommand) anonymizeEntries(entries []history.Entry) []history.Entry {
	anonymized := make([]history.Entry, len(entries))

	for i, entry := range entries {
		anonymized[i] = entry
		anonymized[i].User = "user" + strconv.Itoa(i%10) // Generic user names
		anonymized[i].WorkingDirectory = "/project"
		anonymized[i].Environment = nil // Remove all environment variables

		if anonymized[i].GitInfo != nil {
			anonymized[i].GitInfo.RemoteURL = ""
		}
	}

	return anonymized
}

// cleanHistory removes old entries from history
func (c *HistoryCommand) cleanHistory(workingDir, olderThan, workspace string, all bool) error {
	historyPath := filepath.Join(workingDir, history.HistoryFileName)

	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		return nil // No history file to clean
	}

	// Load current history
	entries, err := c.loadHistoryEntries(workingDir)
	if err != nil {
		return fmt.Errorf("failed to load history: %w", err)
	}

	if all {
		// Remove all entries
		entries = []history.Entry{}
	} else {
		// Parse duration
		duration, err := parseDuration(olderThan)
		if err != nil {
			return fmt.Errorf("invalid duration format: %w", err)
		}

		cutoff := time.Now().Add(-duration)

		// Filter entries
		var filtered []history.Entry
		for _, entry := range entries {
			if entry.Timestamp.After(cutoff) {
				if workspace == "" || entry.Workspace == workspace {
					filtered = append(filtered, entry)
				}
			}
		}
		entries = filtered
	}

	// Save cleaned history
	manager := history.NewManager(workingDir)
	historyFile := &history.HistoryFile{
		Version:   "1.0",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Config: history.Config{
			Enabled:          manager.IsEnabled(),
			MaxEntries:       history.DefaultMaxEntries,
			RetentionDays:    history.DefaultRetentionDays,
			IncludeSensitive: false,
		},
		Entries: entries,
	}

	data, err := json.MarshalIndent(historyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	return os.WriteFile(historyPath, data, 0600)
}

// saveHistoryConfig saves history configuration
func (c *HistoryCommand) saveHistoryConfig(workingDir string, manager *history.Manager) error {
	// This would typically save to a config file
	// For now, we'll create/update the history file with the new config
	historyPath := filepath.Join(workingDir, history.HistoryFileName)

	var historyFile *history.HistoryFile
	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		historyFile = &history.HistoryFile{
			Version:   "1.0",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Config: history.Config{
				Enabled:          manager.IsEnabled(),
				MaxEntries:       history.DefaultMaxEntries,
				RetentionDays:    history.DefaultRetentionDays,
				IncludeSensitive: false,
			},
			Entries: []history.Entry{},
		}
	} else {
		entries, err := c.loadHistoryEntries(workingDir)
		if err != nil {
			return err
		}

		historyFile = &history.HistoryFile{
			Version:   "1.0",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Config: history.Config{
				Enabled:          manager.IsEnabled(),
				MaxEntries:       history.DefaultMaxEntries,
				RetentionDays:    history.DefaultRetentionDays,
				IncludeSensitive: false,
			},
			Entries: entries,
		}
	}

	data, err := json.MarshalIndent(historyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(historyPath, data, 0600)
}

// parseDuration parses duration strings like "30d", "1w", "2h"
func parseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration format")
	}

	valueStr := s[:len(s)-1]
	unit := s[len(s)-1:]

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value: %w", err)
	}

	switch unit {
	case "d":
		return time.Duration(value) * 24 * time.Hour, nil
	case "w":
		return time.Duration(value) * 7 * 24 * time.Hour, nil
	case "h":
		return time.Duration(value) * time.Hour, nil
	case "m":
		return time.Duration(value) * time.Minute, nil
	default:
		return 0, fmt.Errorf("invalid duration unit: %s", unit)
	}
}

// Help returns the help text for the history command
func (c *HistoryCommand) Help() string {
	helpText := `Usage: terraform history <subcommand> [options]

  Track and manage Terraform command execution history.

Available subcommands:
    list     List command history
    show     Show detailed information about a specific command
    export   Export history to file
    clean    Clean old history entries
    enable   Enable history tracking
    disable  Disable history tracking

Global Options:
    -chdir=DIR          Switch to a different working directory

Examples:
    # List recent commands
    terraform history list

    # Show details of a specific command
    terraform history show tf-hist-abc123def456

    # Export history for compliance
    terraform history export --format csv --output report.csv

    # Clean old entries
    terraform history clean --older-than 30d

For more information on a specific subcommand, run:
    terraform history <subcommand> -help
`
	return strings.TrimSpace(helpText)
}

// Synopsis returns a brief description of the history command
func (c *HistoryCommand) Synopsis() string {
	return "Manage Terraform command execution history"
}

// AutocompleteArgs provides command line completion
func (c *HistoryCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

// AutocompleteFlags provides flag completion
func (c *HistoryCommand) AutocompleteFlags() complete.Flags {
	return complete.Flags{
		"-chdir": complete.PredictDirs(""),
	}
}

// runShow implements the "show" subcommand
func (c *HistoryCommand) runShow(args []string) int {
	var (
		verbose bool
		section string
		export  string
	)

	cmdFlags := c.Meta.extendedFlagSet("history show")
	cmdFlags.BoolVar(&verbose, "verbose", false, "Show detailed information")
	cmdFlags.StringVar(&section, "section", "", "Show specific section (plan, state-changes, resources)")
	cmdFlags.StringVar(&export, "export", "", "Export to file")

	if err := cmdFlags.Parse(args); err != nil {
		c.Ui.Error(fmt.Sprintf("Error parsing flags: %s", err))
		return 1
	}

	remainingArgs := cmdFlags.Args()
	var entryID string

	if len(remainingArgs) == 0 {
		// Show most recent entry
		entryID = "latest"
	} else {
		entryID = remainingArgs[0]
	}

	// Get current working directory
	workingDir, err := os.Getwd()
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error getting working directory: %s", err))
		return 1
	}

	// Load history
	entries, err := c.loadHistoryEntries(workingDir)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error loading history: %s", err))
		return 1
	}

	// Find entry
	var entry *history.Entry
	if entryID == "latest" && len(entries) > 0 {
		entry = &entries[0]
	} else {
		for _, e := range entries {
			if e.ID == entryID {
				entry = &e
				break
			}
		}
	}

	if entry == nil {
		c.Ui.Error(fmt.Sprintf("Entry not found: %s", entryID))
		return 1
	}

	// Export if requested
	if export != "" {
		if err := c.exportEntry(entry, export); err != nil {
			c.Ui.Error(fmt.Sprintf("Error exporting entry: %s", err))
			return 1
		}
		c.Ui.Output(fmt.Sprintf("Entry exported to: %s", export))
		return 0
	}

	// Show entry
	c.showEntry(entry, verbose, section)
	return 0
}

// runExport implements the "export" subcommand
func (c *HistoryCommand) runExport(args []string) int {
	var (
		outputFile      string
		format          string
		since           string
		until           string
		commandFilter   string
		workspaceFilter string
		anonymize       bool
	)

	cmdFlags := c.Meta.extendedFlagSet("history export")
	cmdFlags.StringVar(&outputFile, "output", "history_export.json", "Output file path")
	cmdFlags.StringVar(&format, "format", "json", "Export format (json, csv)")
	cmdFlags.StringVar(&since, "since", "", "Export entries since date (YYYY-MM-DD)")
	cmdFlags.StringVar(&until, "until", "", "Export entries until date (YYYY-MM-DD)")
	cmdFlags.StringVar(&commandFilter, "command", "", "Filter by command type")
	cmdFlags.StringVar(&workspaceFilter, "workspace", "", "Filter by workspace")
	cmdFlags.BoolVar(&anonymize, "anonymize", false, "Anonymize sensitive data")

	if err := cmdFlags.Parse(args); err != nil {
		c.Ui.Error(fmt.Sprintf("Error parsing flags: %s", err))
		return 1
	}

	// Get current working directory
	workingDir, err := os.Getwd()
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error getting working directory: %s", err))
		return 1
	}

	// Load history
	entries, err := c.loadHistoryEntries(workingDir)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error loading history: %s", err))
		return 1
	}

	// Apply filters
	filteredEntries := c.filterEntries(entries, filterOptions{
		command:   commandFilter,
		workspace: workspaceFilter,
		since:     since,
		until:     until,
	})

	// Anonymize if requested
	if anonymize {
		filteredEntries = c.anonymizeEntries(filteredEntries)
	}

	// Export
	if err := c.exportEntries(filteredEntries, outputFile, format); err != nil {
		c.Ui.Error(fmt.Sprintf("Error exporting history: %s", err))
		return 1
	}

	c.Ui.Output(fmt.Sprintf("History exported to: %s", outputFile))
	return 0
}

// runClean implements the "clean" subcommand
func (c *HistoryCommand) runClean(args []string) int {
	var (
		olderThan string
		workspace string
		all       bool
		force     bool
	)

	cmdFlags := c.Meta.extendedFlagSet("history clean")
	cmdFlags.StringVar(&olderThan, "older-than", "", "Clean entries older than (e.g., 30d, 1w)")
	cmdFlags.StringVar(&workspace, "workspace", "", "Clean specific workspace")
	cmdFlags.BoolVar(&all, "all", false, "Clean all history")
	cmdFlags.BoolVar(&force, "force", false, "Force clean without confirmation")

	if err := cmdFlags.Parse(args); err != nil {
		c.Ui.Error(fmt.Sprintf("Error parsing flags: %s", err))
		return 1
	}

	if !all && olderThan == "" {
		c.Ui.Error("Either --all or --older-than must be specified")
		return 1
	}

	// Get current working directory
	workingDir, err := os.Getwd()
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error getting working directory: %s", err))
		return 1
	}

	// Load history
	entries, err := c.loadHistoryEntries(workingDir)
	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error loading history: %s", err))
		return 1
	}

	// Confirm if not forced
	if !force {
		c.Ui.Output(fmt.Sprintf("This will remove %d history entries.", len(entries)))
		c.Ui.Output("Are you sure? (yes/no)")

		var response string
		fmt.Scanln(&response)
		if response != "yes" {
			c.Ui.Output("Clean cancelled.")
			return 0
		}
	}

	// Clean history
	if err := c.cleanHistory(workingDir, olderThan, workspace, all); err != nil {
		c.Ui.Error(fmt.Sprintf("Error cleaning history: %s", err))
		return 1
	}

	c.Ui.Output("History cleaned successfully.")
	return 0
}
