package command

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var cmdLogger = log.New(os.Stdout, "CommandDocsLog: ", 0)

type CommandDocs struct{}

func (c *CommandDocs) Help() string {
	return `
Usage: terraform docs <provider> [options] [resource_name]

Lists documentation from provider binary.

Options:
  -l    List all available resources and data sources
  -v    Verbose mode to show file paths
`
}

func (c *CommandDocs) Synopsis() string {
	return "Extracts documentation from provider binary"
}

func (c *CommandDocs) Run(args []string) int {
	if len(args) < 1 {
		fmt.Println("Error: Provider name is required.")
		return 1
	}

	providerName := args[0]
	cmdLogger.Printf("Looking for documentation in provider: %s", providerName)

	// Get provider details from lock file
	providerDetails, err := getProviderFromLockFile(providerName)
	if err != nil {
		cmdLogger.Printf("Error reading lock file: %s", err)
		return 1
	}

	binaryPath := constructBinaryPath(providerDetails)
	cmdLogger.Printf("Using binary at: %s", binaryPath)

	// First pass: scan for documentation paths
	// Find documentation paths
	docPaths, err := findDocumentationPaths(binaryPath)
	if err != nil {
		cmdLogger.Printf("Error scanning binary: %s", err)
		return 1
	}

	if len(docPaths) == 0 {
		cmdLogger.Printf("No documentation paths found in binary")
		return 1
	}

	// Print found paths and their content
	for _, path := range docPaths {
		cmdLogger.Printf("Found documentation path: %s", path)
		content, err := findDocumentationContent(binaryPath, path)
		if err != nil {
			cmdLogger.Printf("Error reading content: %s", err)
			continue
		}
		fmt.Printf("\n=== Content of %s ===\n", path)
		fmt.Println(content)
		fmt.Println("=== End of content ===\n")
	}

	return 0
}

func findDocumentationContent(binaryPath string, path string) (string, error) {
	file, err := os.Open(binaryPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buffer := make([]byte, 8192)
	offset := int64(0)

	cmdLogger.Printf("Searching for content of: %s", path)

	var content bytes.Buffer
	foundStart := false
	for {
		n, err := file.ReadAt(buffer, offset)
		if err != nil && err != io.EOF {
			return "", err
		}
		if n == 0 {
			break
		}

		// Look for the path
		if !foundStart {
			idx := bytes.Index(buffer[:n], []byte(path))
			if idx != -1 {
				cmdLogger.Printf("Found start of file at offset: %d", offset+int64(idx))
				foundStart = true

				// Skip the path itself
				startIdx := idx + len(path)

				// Look for the content start (usually after a newline)
				for i := startIdx; i < n; i++ {
					if buffer[i] == '\n' {
						startIdx = i + 1
						break
					}
				}

				content.Write(buffer[startIdx:n])
			}
		} else {
			// Look for end of file markers
			endIdx := bytes.Index(buffer[:n], []byte("---"))
			if endIdx != -1 {
				content.Write(buffer[:endIdx])
				break
			}
			content.Write(buffer[:n])
		}

		offset += int64(n - 100) // Overlap to avoid missing content at boundaries
		if err == io.EOF {
			break
		}
	}

	if !foundStart {
		return "", fmt.Errorf("content not found for path: %s", path)
	}

	return content.String(), nil
}

func findDocumentationPaths(binaryPath string) ([]string, error) {
	file, err := os.Open(binaryPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// All possible documentation paths and extensions
	pathPatterns := []struct {
		path       string
		extensions []string
	}{
		// Modern structure
		{
			path:       "docs/index",
			extensions: []string{".md"},
		},
		{
			path:       "docs/guides/",
			extensions: []string{".md"},
		},
		{
			path:       "docs/resources/",
			extensions: []string{".md"},
		},
		{
			path:       "docs/data-sources/",
			extensions: []string{".md"},
		},
		{
			path:       "docs/functions/",
			extensions: []string{".md"},
		},
		{
			path:       "docs/ephemeral-resources/",
			extensions: []string{".md"},
		},
		// Legacy structure
		{
			path:       "website/docs/index",
			extensions: []string{".html.markdown", ".html.md"},
		},
		{
			path:       "website/docs/guides/",
			extensions: []string{".html.markdown", ".html.md"},
		},
		{
			path:       "website/docs/r/",
			extensions: []string{".html.markdown", ".html.md"},
		},
		{
			path:       "website/docs/d/",
			extensions: []string{".html.markdown", ".html.md"},
		},
		{
			path:       "website/docs/",
			extensions: []string{".html.markdown", ".html.md"},
		},
		{
			path:       "website/",
			extensions: []string{".html.markdown", ".html.md"},
		},
		// Additional paths
		{
			path:       "doc/",
			extensions: []string{".md", ".html.markdown", ".html.md"},
		},
	}

	buffer := make([]byte, 8192) // Increased buffer size
	var foundPaths []string
	offset := int64(0)

	cmdLogger.Printf("Scanning binary for documentation paths...")

	for {
		n, err := file.ReadAt(buffer, offset)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n == 0 {
			break
		}

		// Convert buffer to string for searching
		content := string(buffer[:n])

		// Look for each path pattern and its extensions
		for _, pattern := range pathPatterns {
			if idx := strings.Index(content, pattern.path); idx != -1 {
				for _, ext := range pattern.extensions {
					// Try to find a complete path
					startIdx := idx
					endIdx := strings.Index(content[idx:], ext)
					if endIdx != -1 {
						path := content[startIdx : startIdx+endIdx+len(ext)]

						// Clean the path
						path = strings.TrimSpace(path)
						// Remove any binary garbage before the actual path
						if lastSlash := strings.LastIndex(path, "/"); lastSlash != -1 {
							pathStart := strings.LastIndex(path[:lastSlash], "docs/")
							if pathStart == -1 {
								pathStart = strings.LastIndex(path[:lastSlash], "doc/")
							}
							if pathStart != -1 {
								path = path[pathStart:]
							}
						}

						// Validate path
						if isValidPath(path) && !contains(foundPaths, path) {
							cmdLogger.Printf("Found path: %s", path)
							foundPaths = append(foundPaths, path)
						}
					}
				}
			}
		}

		offset += int64(n - 100) // Overlap by 100 bytes to avoid missing matches at buffer boundaries
		if err == io.EOF {
			break
		}
	}

	return foundPaths, nil
}

func isValidPath(path string) bool {
	// Check if path starts with expected prefixes
	validPrefixes := []string{
		"docs/",
		"doc/",
		"website/docs/",
	}

	for _, prefix := range validPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func getProviderFromLockFile(providerName string) (*ProviderDetails, error) {
	content, err := os.ReadFile(".terraform.lock.hcl")
	if err != nil {
		return nil, fmt.Errorf("failed to read lock file: %w", err)
	}

	providerRegex := regexp.MustCompile(
		fmt.Sprintf(`provider "([^"]+/%s)" {[^}]*version\s*=\s*"([^"]+)"`,
			providerName))

	matches := providerRegex.FindStringSubmatch(string(content))
	if len(matches) < 3 {
		return nil, fmt.Errorf("provider %s not found in lock file", providerName)
	}

	return &ProviderDetails{
		Source:  matches[1],
		Version: matches[2],
	}, nil
}

type ProviderDetails struct {
	Source  string
	Version string
}

func constructBinaryPath(details *ProviderDetails) string {
	return filepath.Join(
		".terraform",
		"providers",
		details.Source,
		details.Version,
		fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("terraform-provider-%s",
			strings.Split(details.Source, "/")[2]),
	)
}
