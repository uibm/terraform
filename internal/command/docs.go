package command

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var cmdLogger = log.New(os.Stdout, "CommandDocsLog: ", 0)

// DocumentationPaths defines the structure and extensions for provider documentation
var documentationPaths = map[string][]string{
	"docs/":                     {".md"},
	"docs/guides/":              {".md"},
	"docs/resources/":           {".md"},
	"docs/data-sources/":        {".md"},
	"docs/functions/":           {".md"},
	"docs/ephemeral-resources/": {".md"},
	"website/docs/":             {".html.markdown", ".html.md"},
	"website/docs/r/":           {".html.markdown", ".html.md"},
	"website/docs/d/":           {".html.markdown", ".html.md"},
}

type CommandDocs struct{}

type ProviderDetails struct {
	Source  string
	Version string
}

func (c *CommandDocs) Help() string {
	return `
Usage: terraform docs <provider> [options] [resource_name]

Downloads and shows provider documentation.

Options:
  -l    List all available resources and data sources
  -v    Show verbose logging
`
}

func (c *CommandDocs) Synopsis() string {
	return "Downloads and shows provider documentation"
}

func (c *CommandDocs) Run(args []string) int {
	if len(args) < 1 {
		fmt.Println("Error: Provider name is required.")
		return 1
	}

	providerName := args[0]
	cmdLogger.Printf("Fetching documentation for provider: %s", providerName)

	// Get provider details from lock file
	providerDetails, err := getProviderFromLockFile(providerName)
	if err != nil {
		cmdLogger.Printf("Error reading lock file: %s", err)
		return 1
	}

	// Create docs directory under .terraform
	docsDir := filepath.Join(".terraform", "docs", providerName)
	if err := ensureDirectory(docsDir); err != nil {
		cmdLogger.Printf("Error creating docs directory: %s", err)
		return 1
	}

	// Get repository URL
	repoURL := getProviderRepoURL(providerDetails)
	if repoURL == "" {
		// Try registry API as fallback
		source, err := getProviderSourceFromRegistry(providerName)
		if err != nil {
			cmdLogger.Printf("Could not determine repository URL for provider: %s", err)
			return 1
		}
		repoURL = source
	}

	cmdLogger.Printf("Using repository URL: %s", repoURL)

	// Download documentation
	if err := downloadProviderDocs(repoURL, docsDir, providerName); err != nil {
		cmdLogger.Printf("Error downloading documentation: %s", err)
		return 1
	}

	// Handle command options
	if len(args) > 1 {
		if args[1] == "-l" {
			return listResources(docsDir)
		}
		// Show specific resource documentation
		return showResourceDoc(docsDir, args[1])
	}

	return 0
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

func getProviderRepoURL(providerDetails *ProviderDetails) string {
	parts := strings.Split(providerDetails.Source, "/")
	if len(parts) != 3 {
		cmdLogger.Printf("Unexpected provider source format: %s", providerDetails.Source)
		return ""
	}

	organization := parts[1]
	providerName := parts[2]

	if parts[0] == "registry.terraform.io" {
		// Special case for IBM
		if organization == "ibm" || providerName == "ibm" {
			return fmt.Sprintf("https://raw.githubusercontent.com/IBM-Cloud/terraform-provider-%s/main/", providerName)
		}

		return fmt.Sprintf("https://raw.githubusercontent.com/%s/terraform-provider-%s/main/",
			organization, providerName)
	}

	return ""
}

func getProviderSourceFromRegistry(providerName string) (string, error) {
	url := fmt.Sprintf("https://registry.terraform.io/v1/providers/%s", providerName)

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry API returned status: %d", resp.StatusCode)
	}

	var result struct {
		Source string `json:"source"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Source, nil
}

func downloadProviderDocs(repoURL, docsDir, providerName string) error {
	for dirPath, extensions := range documentationPaths {
		targetDir := filepath.Join(docsDir, dirPath)
		if err := ensureDirectory(targetDir); err != nil {
			return err
		}

		// Try to download index file first
		for _, ext := range extensions {
			indexURL := repoURL + dirPath + "index" + ext
			indexPath := filepath.Join(targetDir, "index"+ext)
			err := downloadFile(indexURL, indexPath)
			if err == nil {
				cmdLogger.Printf("Downloaded index file: %s", indexPath)
			}
		}

		// Try to list directory contents (this would need GitHub API integration)
		if strings.Contains(dirPath, "resources") || strings.Contains(dirPath, "r/") {
			// For now, try to download based on common patterns
			for _, ext := range extensions {
				listURL := repoURL + dirPath
				// This is where you'd integrate with GitHub API to get actual file listing
				cmdLogger.Printf("Checking directory: %s", listURL)
			}
		}
	}

	return nil
}

func downloadFile(url, outputPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func ensureDirectory(path string) error {
	return os.MkdirAll(path, os.ModePerm)
}

func listResources(docsDir string) int {
	var resources []string

	// Check both modern and legacy paths
	resourcePaths := []string{
		filepath.Join(docsDir, "docs", "resources"),
		filepath.Join(docsDir, "website", "docs", "r"),
	}

	for _, path := range resourcePaths {
		files, err := os.ReadDir(path)
		if err != nil {
			continue
		}

		for _, file := range files {
			if !file.IsDir() {
				name := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
				name = strings.TrimSuffix(name, ".html")
				resources = append(resources, name)
			}
		}
	}

	// Sort and print resources
	for _, resource := range resources {
		fmt.Printf("* %s\n", resource)
	}

	return 0
}

func showResourceDoc(docsDir, resourceName string) int {
	// Try both modern and legacy paths
	paths := []string{
		filepath.Join(docsDir, "docs", "resources", resourceName+".md"),
		filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.md"),
		filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.markdown"),
	}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err == nil {
			fmt.Println(string(content))
			return 0
		}
	}

	fmt.Printf("Documentation not found for resource: %s\n", resourceName)
	return 1
}
