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
	"sort"
	"strings"
)

var cmdLogger = log.New(os.Stdout, "CommandDocsLog: ", 0)

// GitHubFileInfo represents file information from GitHub API
type GitHubFileInfo struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	DownloadURL string `json:"download_url"`
}
type ProviderDetails struct {
	Source      string
	Version     string
	RepoOwner   string
	RepoName    string
	DocsVersion string
}

type CommandDocs struct{}

func (c *CommandDocs) Help() string {
	return `
Usage: terraform docs <provider> [options] [resource_name]

Downloads and shows provider documentation.

Options:
  -l    List all available resources and data sources
  -v    Show verbose logging

Examples:
  terraform docs aws -l            # List all AWS provider resources
  terraform docs random random_id  # Show documentation for random_id resource
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
	docsDir := filepath.Join(".terraform", "docs", providerName, providerDetails.Version)
	if err := ensureDirectory(docsDir); err != nil {
		cmdLogger.Printf("Error creating docs directory: %s", err)
		return 1
	}

	// Download documentation if it doesn't exist
	if !isDocumentationCached(docsDir) {
		cmdLogger.Printf("Documentation not cached, downloading...")
		if err := downloadProviderDocs(providerDetails, docsDir); err != nil {
			cmdLogger.Printf("Error downloading documentation: %s", err)
			return 1
		}
	}

	// Handle command options
	if len(args) > 1 {
		if args[1] == "-l" {
			return listResources(docsDir)
		}
		return showResourceDoc(docsDir, args[1])
	}

	fmt.Println("Please specify either -l to list resources or provide a resource name")
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

	parts := strings.Split(matches[1], "/")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid provider source format: %s", matches[1])
	}

	details := &ProviderDetails{
		Source:      matches[1],
		Version:     matches[2],
		RepoOwner:   parts[1],
		RepoName:    parts[2], // Changed: Don't add terraform-provider- prefix
		DocsVersion: "main",
	}

	// Special case for IBM
	if details.RepoOwner == "ibm" {
		details.RepoOwner = "IBM-Cloud"
		details.RepoName = "terraform-provider-ibm" // Set full name for IBM
	} else {
		repoName := details.RepoName
		details.RepoName = fmt.Sprintf("terraform-provider-%s", repoName)
	}

	cmdLogger.Printf("Provider details: Owner=%s, Repo=%s, Version=%s",
		details.RepoOwner, details.RepoName, details.Version)

	return details, nil
}

func downloadProviderDocs(details *ProviderDetails, docsDir string) error {
	cmdLogger.Printf("=== Starting Documentation Download ===")
	cmdLogger.Printf("Provider: %s/%s (Version: %s)", details.RepoOwner, details.RepoName, details.Version)
	cmdLogger.Printf("Output Directory: %s", docsDir)
	cmdLogger.Printf("=====================================")

	// Try both modern and legacy documentation paths
	paths := []string{
		"docs",
		"website/docs",
	}

	totalFound := 0
	totalDownloaded := 0
	totalErrors := 0

	for _, basePath := range paths {
		cmdLogger.Printf("\n=== Checking Base Path: %s ===", basePath)

		files, err := listGitHubContents(details, basePath)
		if err != nil {
			cmdLogger.Printf("❌ Error listing contents for %s: %s", basePath, err)
			totalErrors++
			continue
		}

		if len(files) == 0 {
			cmdLogger.Printf("📝 No files found in %s", basePath)
			continue
		}

		cmdLogger.Printf("📂 Found %d items in %s", len(files), basePath)
		totalFound += len(files)

		// Process each file/directory
		for _, file := range files {
			cmdLogger.Printf("\n--- Processing: %s ---", file.Path)
			cmdLogger.Printf("Type: %s", file.Type)
			if file.DownloadURL != "" {
				cmdLogger.Printf("Download URL: %s", file.DownloadURL)
			}

			if file.Type == "dir" {
				cmdLogger.Printf("📂 Entering directory: %s", file.Path)
				subFiles, err := listGitHubContents(details, file.Path)
				if err != nil {
					cmdLogger.Printf("❌ Error listing contents for %s: %s", file.Path, err)
					totalErrors++
					continue
				}

				cmdLogger.Printf("📂 Found %d files in subdirectory %s", len(subFiles), file.Path)
				totalFound += len(subFiles)

				for _, subFile := range subFiles {
					cmdLogger.Printf("\n---> Examining: %s", subFile.Path)
					cmdLogger.Printf("Type: %s", subFile.Type)

					if subFile.Type == "file" {
						if shouldDownloadFile(subFile.Name) {
							cmdLogger.Printf("📄 Attempting to download: %s", subFile.Path)
							outputPath := filepath.Join(docsDir, subFile.Path)
							if err := downloadGitHubFile(subFile.DownloadURL, outputPath); err != nil {
								cmdLogger.Printf("❌ Error downloading %s: %s", subFile.Path, err)
								totalErrors++
							} else {
								cmdLogger.Printf("✅ Successfully downloaded: %s", subFile.Path)
								totalDownloaded++
							}
						} else {
							cmdLogger.Printf("⏭️ Skipping non-documentation file: %s", subFile.Name)
						}
					} else {
						cmdLogger.Printf("📂 Skipping nested directory: %s", subFile.Path)
					}
				}
			} else if file.Type == "file" {
				if shouldDownloadFile(file.Name) {
					cmdLogger.Printf("📄 Attempting to download: %s", file.Path)
					outputPath := filepath.Join(docsDir, file.Path)
					if err := downloadGitHubFile(file.DownloadURL, outputPath); err != nil {
						cmdLogger.Printf("❌ Error downloading %s: %s", file.Path, err)
						totalErrors++
					} else {
						cmdLogger.Printf("✅ Successfully downloaded: %s", file.Path)
						totalDownloaded++
					}
				} else {
					cmdLogger.Printf("⏭️ Skipping non-documentation file: %s", file.Name)
				}
			}
		}
	}

	cmdLogger.Printf("\n=== Download Summary ===")
	cmdLogger.Printf("Total items found: %d", totalFound)
	cmdLogger.Printf("Total files downloaded: %d", totalDownloaded)
	cmdLogger.Printf("Total errors encountered: %d", totalErrors)
	cmdLogger.Printf("=====================")

	if totalDownloaded == 0 {
		return fmt.Errorf("no documentation files were downloaded (found: %d, errors: %d)",
			totalFound, totalErrors)
	}

	return nil
}

func shouldDownloadFile(filename string) bool {
	extensions := []string{".md", ".markdown", ".html.md", ".html.markdown"}
	lowername := strings.ToLower(filename)

	cmdLogger.Printf("Checking file extension: %s", filename)
	for _, ext := range extensions {
		if strings.HasSuffix(lowername, ext) {
			cmdLogger.Printf("✅ File %s matches extension %s", filename, ext)
			return true
		}
	}
	cmdLogger.Printf("❌ File %s does not match any documentation extensions", filename)
	return false
}

func listGitHubContents(details *ProviderDetails, path string) ([]GitHubFileInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s",
		details.RepoOwner, details.RepoName, path)

	cmdLogger.Printf("\n=== GitHub API Request ===")
	cmdLogger.Printf("URL: %s", url)
	cmdLogger.Printf("Method: GET")

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Terraform-Docs-Command")

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		cmdLogger.Printf("Using GitHub token for authentication")
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		cmdLogger.Printf("⚠️ No GitHub token found - rate limits may apply")
	}

	cmdLogger.Printf("Making request...")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	cmdLogger.Printf("Response Status: %d", resp.StatusCode)

	if resp.StatusCode == http.StatusNotFound {
		cmdLogger.Printf("Path %s not found in repository", path)
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error: Status: %d, Body: %s",
			resp.StatusCode, string(body))
	}

	var files []GitHubFileInfo
	if err := json.Unmarshal(body, &files); err != nil {
		// Try single file response
		var file GitHubFileInfo
		if err := json.Unmarshal(body, &file); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		files = []GitHubFileInfo{file}
	}

	cmdLogger.Printf("Found %d items in response\n", len(files))
	for _, file := range files {
		cmdLogger.Printf("- %s (Type: %s)", file.Path, file.Type)
	}

	return files, nil
}

func downloadGitHubFile(url string, outputPath string) error {
	cmdLogger.Printf("\n=== Downloading File ===")
	cmdLogger.Printf("From: %s", url)
	cmdLogger.Printf("To: %s", outputPath)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	cmdLogger.Printf("Download Status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %s", resp.Status)
	}

	if err := ensureDirectory(filepath.Dir(outputPath)); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	size, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	cmdLogger.Printf("✅ Successfully wrote %d bytes to %s", size, outputPath)
	return nil
}
func ensureDirectory(path string) error {
	return os.MkdirAll(path, os.ModePerm)
}

func isDocumentationCached(docsDir string) bool {
	_, err := os.Stat(filepath.Join(docsDir, "docs"))
	if err == nil {
		return true
	}
	_, err = os.Stat(filepath.Join(docsDir, "website", "docs"))
	return err == nil
}

func listResources(docsDir string) int {
	var resources []string

	// Check both modern and legacy paths
	resourcePaths := []string{
		filepath.Join(docsDir, "docs", "resources"),
		filepath.Join(docsDir, "website", "docs", "r"),
		filepath.Join(docsDir, "docs", "data-sources"),
		filepath.Join(docsDir, "website", "docs", "d"),
	}

	for _, path := range resourcePaths {
		err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if !info.IsDir() && (strings.HasSuffix(info.Name(), ".md") ||
				strings.HasSuffix(info.Name(), ".markdown")) {
				name := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
				name = strings.TrimSuffix(name, ".html")
				resources = append(resources, name)
			}
			return nil
		})
		if err != nil {
			cmdLogger.Printf("Error walking path %s: %s", path, err)
		}
	}

	// Sort and print resources
	sort.Strings(resources)
	for _, resource := range resources {
		fmt.Printf("* %s\n", resource)
	}

	return 0
}

func showResourceDoc(docsDir, resourceName string) int {
	paths := []string{
		filepath.Join(docsDir, "docs", "resources", resourceName+".md"),
		filepath.Join(docsDir, "docs", "data-sources", resourceName+".md"),
		filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.md"),
		filepath.Join(docsDir, "website", "docs", "d", resourceName+".html.md"),
		filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.markdown"),
		filepath.Join(docsDir, "website", "docs", "d", resourceName+".html.markdown"),
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
