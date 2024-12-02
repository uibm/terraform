package command

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var cmdLogger = log.New(os.Stdout, "CommandDocsLog: ", 0)

type CommandDocs struct{}

type ProviderDetails struct {
	Source      string
	Version     string
	RepoOwner   string
	RepoName    string
	DocsVersion string
}

func (c *CommandDocs) Help() string {
	return `
Usage: terraform docs <provider> [options] [resource_name]

Shows provider documentation for resources and data sources.

Options:
  -l    List all available resources and data sources

Examples:
  terraform docs aws -l            # List all AWS provider resources
  terraform docs random random_id  # Show documentation for random_id resource
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
	cmdLogger.Printf("Fetching documentation for provider: %s", providerName)

	// Get provider details from lock file
	providerDetails, err := getProviderFromLockFile(providerName)
	if err != nil {
		cmdLogger.Printf("Error reading lock file: %s", err)
		return 1
	}
	cmdLogger.Printf("Provider details found: owner=%s, repo=%s",
		providerDetails.RepoOwner, providerDetails.RepoName)

	docsDir := filepath.Join(".terraform", "docs", providerName)
	cmdLogger.Printf("Using documentation directory: %s", docsDir)

	if err := ensureDirectory(docsDir); err != nil {
		cmdLogger.Printf("Error creating docs directory: %s", err)
		return 1
	}

	if !isDocumentationCached(docsDir) {
		cmdLogger.Printf("Documentation not cached, cloning repository...")
		if err := cloneAndOrganizeDocs(providerDetails, docsDir); err != nil {
			cmdLogger.Printf("Error preparing documentation: %s", err)
			return 1
		}
	}

	// Handle command options
	if len(args) > 1 {
		if args[1] == "-l" {
			cmdLogger.Printf("Listing resources from: %s", docsDir)
			return listResources(docsDir)
		}

		resourceName := args[1]
		resourceType := "both" // Default to searching both types

		// Check for resource type flag
		if len(args) > 2 {
			switch args[2] {
			case "-d":
				resourceType = "data"
				cmdLogger.Printf("Looking for data source: %s", resourceName)
			case "-r":
				resourceType = "resource"
				cmdLogger.Printf("Looking for resource: %s", resourceName)
			default:
				fmt.Println("Invalid flag. Please use -d for data source or -r for resource")
				return 1
			}
		}

		return showResourceDoc(docsDir, resourceName, resourceType)
	}

	fmt.Println("Please specify either -l to list resources or provide a resource name with -d/-r flag")
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
		RepoName:    parts[2],
		DocsVersion: "main",
	}

	// Handle special cases
	if details.RepoOwner == "ibm" {
		details.RepoOwner = "IBM-Cloud"
	}

	// Add terraform-provider- prefix if not present
	if !strings.HasPrefix(details.RepoName, "terraform-provider-") {
		details.RepoName = "terraform-provider-" + details.RepoName
	}

	return details, nil
}
func cloneAndOrganizeDocs(details *ProviderDetails, docsDir string) error {
	// Create temporary directory for cloning
	tmpDir, err := os.MkdirTemp("", "terraform-provider-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	cmdLogger.Printf("Created temporary directory: %s", tmpDir)
	defer os.RemoveAll(tmpDir)

	// Construct repository URL
	repoURL := fmt.Sprintf("https://github.com/%s/%s.git",
		details.RepoOwner, details.RepoName)
	cmdLogger.Printf("Cloning from: %s", repoURL)

	// Clone repository
	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to clone repository: %s: %s", err, string(output))
	}
	cmdLogger.Printf("Successfully cloned repository")

	// Look for documentation in known locations
	docsPaths := []struct {
		src  string
		dest string
	}{
		{filepath.Join(tmpDir, "docs"), filepath.Join(docsDir, "docs")},
		{filepath.Join(tmpDir, "website", "docs"), filepath.Join(docsDir, "website", "docs")},
	}

	foundDocs := false
	for _, path := range docsPaths {
		cmdLogger.Printf("Checking for docs in: %s", path.src)
		if _, err := os.Stat(path.src); err == nil {
			cmdLogger.Printf("Found documentation in %s", path.src)
			if err := copyDir(path.src, path.dest); err != nil {
				cmdLogger.Printf("Error copying docs from %s: %s", path.src, err)
				continue
			}
			foundDocs = true
			cmdLogger.Printf("Copied documentation to: %s", path.dest)
		} else {
			cmdLogger.Printf("No documentation found in: %s", path.src)
		}
	}

	if !foundDocs {
		return fmt.Errorf("no documentation found in repository")
	}

	return nil
}

func copyDir(src, dst string) error {
	if err := ensureDirectory(dst); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		sourcePath := filepath.Join(src, entry.Name())
		destPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(sourcePath, destPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(sourcePath, destPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func ensureDirectory(path string) error {
	return os.MkdirAll(path, os.ModePerm)
}

func isDocumentationCached(docsDir string) bool {
	// Check for actual content in either docs or website/docs
	for _, subDir := range []string{"docs", "website/docs"} {
		path := filepath.Join(docsDir, subDir)
		if _, err := os.Stat(path); err == nil {
			// Verify there are actual files
			entries, err := os.ReadDir(path)
			if err == nil && len(entries) > 0 {
				cmdLogger.Printf("Found existing documentation in: %s", path)
				return true
			}
		}
	}
	cmdLogger.Printf("No valid documentation cache found in: %s", docsDir)
	return false
}

func listResources(docsDir string) int {
	cmdLogger.Printf("Searching for resources in: %s", docsDir)

	// Keep resources and data sources separate
	resources := make([]string, 0)
	dataSources := make([]string, 0)

	// Modern structure
	modernPaths := map[string]*[]string{
		filepath.Join(docsDir, "docs", "resources"):    &resources,
		filepath.Join(docsDir, "docs", "data-sources"): &dataSources,
	}

	// Check modern paths
	for path, slice := range modernPaths {
		cmdLogger.Printf("Checking path: %s", path)
		err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				cmdLogger.Printf("Error accessing %s: %s", path, err)
				return nil
			}
			if !info.IsDir() && isDocumentationFile(info.Name()) {
				name := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
				*slice = append(*slice, name)
				cmdLogger.Printf("Found: %s", name)
			}
			return nil
		})
		if err != nil {
			cmdLogger.Printf("Error walking path %s: %s", path, err)
		}
	}

	// Print resources by type
	if len(resources) > 0 {
		fmt.Println("\nResources:")
		sort.Strings(resources)
		for _, resource := range resources {
			fmt.Printf("* %s\n", resource)
		}
	}

	if len(dataSources) > 0 {
		fmt.Println("\nData Sources:")
		sort.Strings(dataSources)
		for _, dataSource := range dataSources {
			fmt.Printf("* %s\n", dataSource)
		}
	}

	cmdLogger.Printf("Found %d resources and %d data sources",
		len(resources), len(dataSources))
	return 0
}

func showResourceDoc(docsDir, resourceName, resourceType string) int {
	var paths []string

	switch resourceType {
	case "data":
		// Only check data source paths
		paths = []string{
			filepath.Join(docsDir, "docs", "data-sources", resourceName+".md"),
			filepath.Join(docsDir, "website", "docs", "d", resourceName+".html.md"),
			filepath.Join(docsDir, "website", "docs", "d", resourceName+".html.markdown"),
		}
	case "resource":
		// Only check resource paths
		paths = []string{
			filepath.Join(docsDir, "docs", "resources", resourceName+".md"),
			filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.md"),
			filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.markdown"),
		}
	default:
		// Check all paths (for backward compatibility)
		paths = []string{
			filepath.Join(docsDir, "docs", "resources", resourceName+".md"),
			filepath.Join(docsDir, "docs", "data-sources", resourceName+".md"),
			filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.md"),
			filepath.Join(docsDir, "website", "docs", "d", resourceName+".html.md"),
			filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.markdown"),
			filepath.Join(docsDir, "website", "docs", "d", resourceName+".html.markdown"),
		}
	}

	cmdLogger.Printf("Searching for documentation in the following paths:")
	for _, path := range paths {
		cmdLogger.Printf("- %s", path)
	}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err == nil {
			cmdLogger.Printf("Found documentation at: %s", path)
			fmt.Println(string(content))
			return 0
		}
	}

	switch resourceType {
	case "data":
		fmt.Printf("Documentation not found for data source: %s\n", resourceName)
	case "resource":
		fmt.Printf("Documentation not found for resource: %s\n", resourceName)
	default:
		fmt.Printf("Documentation not found for: %s\n", resourceName)
	}
	return 1
}

// func showResourceDoc(docsDir, resourceName string, isDataSource bool) int {
// 	var docPath string
// 	if isDataSource {
// 		docPath = filepath.Join(docsDir, "docs", "data-sources", resourceName+".md")
// 	} else {
// 		docPath = filepath.Join(docsDir, "docs", "resources", resourceName+".md")
// 	}

// 	content, err := os.ReadFile(docPath)
// 	if err != nil {
// 		fmt.Printf("Documentation not found for %s: %s\n",
// 			resourceName, err)
// 		return 1
// 	}

// 	fmt.Println(string(content))
// 	return 0
// }

// func showResourceDoc(docsDir, resourceName string) int {
// 	paths := []string{
// 		filepath.Join(docsDir, "docs", "resources", resourceName+".md"),
// 		filepath.Join(docsDir, "docs", "data-sources", resourceName+".md"),
// 		filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.md"),
// 		filepath.Join(docsDir, "website", "docs", "d", resourceName+".html.md"),
// 		filepath.Join(docsDir, "website", "docs", "r", resourceName+".html.markdown"),
// 		filepath.Join(docsDir, "website", "docs", "d", resourceName+".html.markdown"),
// 	}

// 	for _, path := range paths {
// 		content, err := os.ReadFile(path)
// 		if err == nil {
// 			fmt.Println(string(content))
// 			return 0
// 		}
// 	}

// 	fmt.Printf("Documentation not found for resource: %s\n", resourceName)
// 	return 1
// }

func isDocumentationFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".md" || ext == ".markdown" ||
		strings.HasSuffix(filename, ".html.md") ||
		strings.HasSuffix(filename, ".html.markdown")
}
