package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		showHelp()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "set":
		setVersion()
	case "bump":
		if len(os.Args) < 3 {
			fmt.Println("Error: Must specify bump type (major, minor, patch)")
			os.Exit(1)
		}
		if err := bumpVersion(os.Args[2]); err != nil {
			fmt.Printf("❌ Version bump failed: %v\n", err)
			os.Exit(1)
		}
	default:
		showHelp()
		os.Exit(1)
	}
}

func showHelp() {
	fmt.Println("Version management tool")
	fmt.Println("Usage:")
	fmt.Println("  version set        - Set version from git describe")
	fmt.Println("  version bump TYPE  - Bump version (major, minor, patch)")
}

func getVersionFile() string {
	return "runtime/version/version.txt"
}

func getCurrentDescribeVersion() (string, error) {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git version: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func setVersion() {
	version, err := getCurrentDescribeVersion()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	filePath := getVersionFile()
	if err := os.WriteFile(filePath, []byte(version), 0644); err != nil {
		fmt.Printf("Error writing version file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Version set to: %s\n", version)
}

func bumpVersion(bumpType string) error {
	tx := newBumpTransaction()

	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️  Panic during version bump: %v\n", r)
			tx.Rollback()
			os.Exit(1)
		} else if tx.hasError {
			tx.Rollback()
		}
	}()

	if !isGitRepository() {
		return fmt.Errorf("not in a git repository")
	}

	if hasUncommittedChanges() {
		return fmt.Errorf("cannot create release with uncommitted changes. Please commit or stash your changes first")
	}

	currentVersion, err := getCurrentTagVersion()
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}
	fmt.Printf("Current version: %s\n", currentVersion)
	tx.currentVersion = currentVersion

	newVersion, err := calculateNewVersion(currentVersion, bumpType)
	if err != nil {
		return fmt.Errorf("failed to calculate new version: %w", err)
	}
	fmt.Printf("New version will be: %s\n", newVersion)
	tx.newVersion = newVersion

	if err := updateVersionFile(newVersion); err != nil {
		return fmt.Errorf("failed to update version file: %w", err)
	}
	tx.versionFileUpdated = true

	if err := commitVersionFile(newVersion); err != nil {
		return fmt.Errorf("failed to commit version changes: %w", err)
	}
	tx.commitCreated = true

	tx.markSuccessful()

	fmt.Printf("\n✅ Version %s committed successfully!\n", newVersion)
	fmt.Printf("   Push main with: git push\n")
	fmt.Printf("   Then run the Release workflow; it creates the tag.\n")
	return nil
}

func isGitRepository() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}

func hasUncommittedChanges() bool {
	versionFilePath := getVersionFile()

	cmd := exec.Command("git", "status", "--porcelain")
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Git error checking for uncommitted changes: %s\n", string(output))
		return true
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		if !strings.HasSuffix(trimmedLine, versionFilePath) {
			return true
		}
	}

	return false
}

func getCurrentTagVersion() (string, error) {
	exec.Command("git", "fetch", "--tags").Run()

	cmd := exec.Command("git", "tag", "--sort=-v:refname")
	output, err := cmd.CombinedOutput()
	if err != nil || len(strings.TrimSpace(string(output))) == 0 {
		content, readErr := os.ReadFile(getVersionFile())
		if readErr != nil {
			return "", fmt.Errorf("read version file: %w", readErr)
		}
		return strings.TrimSpace(string(content)), nil
	}

	tags := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(tags) == 0 || tags[0] == "" {
		return "", fmt.Errorf("no version tag found")
	}

	return tags[0], nil
}

func calculateNewVersion(currentVersion, bumpType string) (string, error) {
	if !strings.HasPrefix(currentVersion, "v") {
		return "", fmt.Errorf("invalid version format: missing 'v' prefix in '%s'", currentVersion)
	}
	tag := strings.TrimPrefix(currentVersion, "v")

	parts := strings.Split(tag, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("cannot bump non-stable version %q; use the release workflow", currentVersion)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid major version in '%s': %w", tag, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid minor version in '%s': %w", tag, err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", fmt.Errorf("invalid patch version in '%s': %w", tag, err)
	}

	switch bumpType {
	case "major":
		major++
		minor = 0
		patch = 0
	case "minor":
		minor++
		patch = 0
	case "patch":
		patch++
	default:
		return "", fmt.Errorf("unknown bump type '%s'. Use 'major', 'minor', or 'patch'", bumpType)
	}

	return fmt.Sprintf("v%d.%d.%d", major, minor, patch), nil
}

func updateVersionFile(newVersion string) error {
	fmt.Printf("📝 Updating version file to %s...\n", newVersion)
	return os.WriteFile(getVersionFile(), []byte(newVersion), 0644)
}

func commitVersionFile(newVersion string) error {
	fmt.Println("📦 Committing version...")

	cmd := exec.Command(
		"git",
		"add",
		getVersionFile(),
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to stage version files: %w\nOutput: %s", err, string(output))
	}

	cmd = exec.Command("git", "commit", "-S", "-m", fmt.Sprintf("chore: release %s", newVersion))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to commit version file: %w\nOutput: %s", err, string(output))
	}

	return nil
}

type bumpTransaction struct {
	currentVersion     string
	newVersion         string
	versionFileUpdated bool
	commitCreated      bool
	hasError           bool
	successful         bool
}

func newBumpTransaction() *bumpTransaction {
	return &bumpTransaction{
		hasError:   true,
		successful: false,
	}
}

func (tx *bumpTransaction) markSuccessful() {
	tx.successful = true
	tx.hasError = false
}

func (tx *bumpTransaction) Rollback() {
	fmt.Println("\n🔄 Rolling back version bump...")

	if tx.commitCreated {
		fmt.Println("   Resetting last commit...")
		exec.Command("git", "reset", "HEAD~1").Run()
	}

	if tx.versionFileUpdated {
		fmt.Printf("   Restoring version file to %s...\n", tx.currentVersion)
		os.WriteFile(getVersionFile(), []byte(tx.currentVersion), 0644)
	}

	fmt.Println("   Rollback completed.")
}
