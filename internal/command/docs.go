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

	// Create docs directory under .terraform
	docsDir := filepath.Join(".terraform", "docs", providerName, providerDetails.Version)
	if err := ensureDirectory(docsDir); err != nil {
		cmdLogger.Printf("Error creating docs directory: %s", err)
		return 1
	}

	// Only clone if docs don't exist
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
	defer os.RemoveAll(tmpDir)

	// Construct repository URL
	repoURL := fmt.Sprintf("https://github.com/%s/%s.git",
		details.RepoOwner, details.RepoName)

	cmdLogger.Printf("Cloning %s...", repoURL)

	// Clone repository
	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to clone repository: %s", string(output))
	}

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
		if _, err := os.Stat(path.src); err == nil {
			cmdLogger.Printf("Found documentation in %s", path.src)
			if err := copyDir(path.src, path.dest); err != nil {
				cmdLogger.Printf("Error copying docs from %s: %s", path.src, err)
				continue
			}
			foundDocs = true
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
	for _, subDir := range []string{"docs", "website/docs"} {
		if _, err := os.Stat(filepath.Join(docsDir, subDir)); err == nil {
			return true
		}
	}
	return false
}

func listResources(docsDir string) int {
	var resources []string

	// Check both modern and legacy paths
	resourcePaths := []string{
		filepath.Join(docsDir, "docs", "resources"),
		filepath.Join(docsDir, "docs", "data-sources"),
		filepath.Join(docsDir, "website", "docs", "r"),
		filepath.Join(docsDir, "website", "docs", "d"),
	}

	for _, path := range resourcePaths {
		err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if !info.IsDir() && isDocumentationFile(info.Name()) {
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

func isDocumentationFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".md" || ext == ".markdown" ||
		strings.HasSuffix(filename, ".html.md") ||
		strings.HasSuffix(filename, ".html.markdown")
}
