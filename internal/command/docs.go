package command

/*
Function Overview:
1. Command Interface Functions:
   - Help() - Returns help text for the command
   - Synopsis() - Returns a brief description
   - Run(args []string) - Main entry point

2. Provider Detection Functions:
   - getProviderFromLockFile(providerName string) - Reads provider info from lock file
   - constructBinaryPath(details *ProviderDetails) - Builds path to provider binary

3. Binary Analysis Functions:
   - readProviderSchema(binaryPath string) - Reads schema from provider binary
   - findResources(data []byte, marker []byte) - Finds resource definitions
   - isValidResourceName(name string) - Validates resource names

4. Helper Functions:
   - listDirectoryContents(dirPath string) - Debug helper to list directory contents
*/

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ----------------------------------------------------------------------------
// Types and Constants
// ----------------------------------------------------------------------------

var cmdLogger = log.New(os.Stdout, "CommandDocsLog: ", 0)

type CommandDocs struct{}

type ProviderDetails struct {
	Source  string
	Version string
}

// ----------------------------------------------------------------------------
// Command Interface Implementation
// ----------------------------------------------------------------------------

func (c *CommandDocs) Help() string {
	return `
Usage: terraform docs <provider> [options] [resource_name]

Lists documentation for provider resources and data sources.

Options:
  -l    List all available resources and data sources

Examples:
  terraform docs random -l         # Lists all random provider resources
  terraform docs random random_id  # Shows documentation for random_id resource
`
}

func (c *CommandDocs) Synopsis() string {
	return "Shows provider documentation for resources and data sources"
}

func (c *CommandDocs) Run(args []string) int {
	if len(args) < 1 {
		fmt.Println("Error: Provider name is required.")
		return 1
	}

	providerName := args[0]
	cmdLogger.Printf("Provider name: %s", providerName)

	// Get provider details from lock file
	providerDetails, err := getProviderFromLockFile(providerName)
	if err != nil {
		cmdLogger.Printf("Error reading lock file: %s", err)
		return 1
	}

	// Construct binary path
	binaryPath := constructBinaryPath(providerDetails)
	cmdLogger.Printf("Looking for binary at: %s", binaryPath)

	if _, err := os.Stat(binaryPath); err != nil {
		cmdLogger.Printf("Error accessing binary: %s", err)
		return 1
	}

	cmdLogger.Printf("Found provider binary at: %s", binaryPath)

	// Handle different command modes
	if len(args) > 1 {
		if args[1] == "-l" {
			cmdLogger.Printf("List mode enabled - attempting to list resources")
			return readProviderSchema(binaryPath, providerName)
		} else {
			cmdLogger.Printf("Documentation mode - attempting to show resource docs")
			return showResourceDocs(binaryPath, args[1], providerName)
		}
	}

	fmt.Println("Please specify either -l to list resources or provide a resource name")
	return 1
}

// ----------------------------------------------------------------------------
// Provider Detection Functions
// ----------------------------------------------------------------------------

func getProviderFromLockFile(providerName string) (*ProviderDetails, error) {
	cmdLogger.Printf("Reading lock file for provider: %s", providerName)

	content, err := ioutil.ReadFile(".terraform.lock.hcl")
	if err != nil {
		return nil, fmt.Errorf("failed to read lock file: %w", err)
	}

	details := &ProviderDetails{}

	// Look for provider block
	providerRegex := regexp.MustCompile(`provider "([^"]+/[^"]+/` + providerName + `)" {[^}]*version\s*=\s*"([^"]+)"`)
	matches := providerRegex.FindStringSubmatch(string(content))

	if len(matches) < 3 {
		return nil, fmt.Errorf("provider %s not found in lock file", providerName)
	}

	details.Source = matches[1]
	details.Version = matches[2]

	cmdLogger.Printf("Found provider: %s version %s", details.Source, details.Version)
	return details, nil
}

func constructBinaryPath(details *ProviderDetails) string {
	// Determine OS and architecture
	os := runtime.GOOS
	arch := runtime.GOARCH

	// Construct the path
	return filepath.Join(
		".terraform",
		"providers",
		details.Source,
		details.Version,
		fmt.Sprintf("%s_%s", os, arch),
		fmt.Sprintf("terraform-provider-%s", strings.Split(details.Source, "/")[2]),
	)
}

// ----------------------------------------------------------------------------
// Binary Analysis Functions
// ----------------------------------------------------------------------------

func readProviderSchema(binaryPath, providerName string) int {
	cmdLogger.Printf("Attempting to read schema from binary: %s", binaryPath)

	// Open the binary file
	f, err := os.Open(binaryPath)
	if err != nil {
		cmdLogger.Printf("Error opening binary: %s", err)
		return 1
	}
	defer f.Close()

	// Read the first few bytes to determine the header
	header := make([]byte, 8)
	if _, err := f.Read(header); err != nil {
		cmdLogger.Printf("Error reading header: %s", err)
		return 1
	}

	cmdLogger.Printf("Binary header: %x", header)

	// Try to find the schema section
	buffer := make([]byte, 1024*1024) // 1MB buffer
	for {
		n, err := f.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			cmdLogger.Printf("Error reading binary: %s", err)
			return 1
		}

		// Look for schema-related markers in the binary
		resourceMarker := []byte("Resource")
		dataSourceMarker := []byte("DataSource")

		// Search for resource and data source definitions
		data := buffer[:n]
		resources := findResources(data, resourceMarker)
		dataSources := findResources(data, dataSourceMarker)

		// Print found resources
		for _, resource := range resources {
			fmt.Printf("* %s_%s\n", strings.ToLower(providerName), resource)
		}
		for _, dataSource := range dataSources {
			fmt.Printf("* data_%s_%s\n", strings.ToLower(providerName), dataSource)
		}
	}

	return 0
}

func findResources(data []byte, marker []byte) []string {
	var resources []string
	offset := 0

	for {
		// Find the marker
		idx := bytes.Index(data[offset:], marker)
		if idx == -1 {
			break
		}

		// Move past the marker
		offset += idx + len(marker)

		// Try to read the resource name
		end := bytes.IndexByte(data[offset:], 0)
		if end == -1 {
			break
		}

		name := string(data[offset : offset+end])
		if isValidResourceName(name) {
			resources = append(resources, name)
		}

		offset += end + 1
	}

	return resources
}

func isValidResourceName(name string) bool {
	// Add validation logic for resource names
	return len(name) > 0 && !strings.Contains(name, " ")
}

func showResourceDocs(binaryPath, resourceName, providerName string) int {
	cmdLogger.Printf("Attempting to read documentation for resource: %s", resourceName)
	// This function needs to be implemented based on how we want to extract
	// documentation from the binary
	return 1
}

// ----------------------------------------------------------------------------
// Helper Functions
// ----------------------------------------------------------------------------

func listDirectoryContents(dirPath string) {
	cmdLogger.Printf("Directory contents for: %s", dirPath)
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			cmdLogger.Printf("Error accessing path %s: %v", path, err)
			return err
		}
		rel, err := filepath.Rel(dirPath, path)
		if err != nil {
			rel = path
		}
		if info.IsDir() {
			cmdLogger.Printf("    DIR: %s", rel)
		} else {
			cmdLogger.Printf("    FILE: %s (%d bytes)", rel, info.Size())
		}
		return nil
	})
	if err != nil {
		cmdLogger.Printf("Error walking directory: %v", err)
	}
}
