// Package agentinstall downloads the Aviato agent binary for a platform.
package agentinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DefaultURLTemplate points at the GitHub releases of the agent. Placeholders: {release}
// ("latest/download" or "download/<version>"), {version}, {os}, {arch} and {ext}.
const DefaultURLTemplate = "https://github.com/getaviato/aviato/releases/{release}/aviato-agent-{os}-{arch}{ext}"

// Options configure [Install].
type Options struct {
	// URLTemplate defaults to DefaultURLTemplate.
	URLTemplate string
	// Version is a release tag, or "latest" (the default).
	Version string
	// GOOS and GOARCH select the platform (Go names).
	GOOS   string
	GOARCH string
	// Output is the path of the installed binary.
	Output string
	// SHA256 is the expected hex checksum of the binary; not checked when empty.
	SHA256 string
	// Client defaults to http.DefaultClient.
	Client *http.Client
}

// Platform maps Go platform names to the agent's release asset names.
func Platform(goos, goarch string) (osName, arch, ext string, err error) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "linux", "x64", "", nil
	case "linux/arm64":
		return "linux", "arm64", "", nil
	case "darwin/arm64":
		return "darwin", "arm64", "", nil
	case "windows/amd64":
		return "windows", "x64", ".exe", nil
	default:
		return "", "", "", fmt.Errorf("no agent binary for %s/%s (available: linux/amd64, linux/arm64, darwin/arm64, windows/amd64)", goos, goarch)
	}
}

// URL resolves the download URL of the agent binary.
func URL(template, version, goos, goarch string) (string, error) {
	osName, arch, ext, err := Platform(goos, goarch)
	if err != nil {
		return "", err
	}
	if template == "" {
		template = DefaultURLTemplate
	}
	if version == "" {
		version = "latest"
	}
	release := "download/" + version
	if version == "latest" {
		release = "latest/download"
	}
	return strings.NewReplacer("{release}", release, "{version}", version, "{os}", osName, "{arch}", arch, "{ext}", ext).Replace(template), nil
}

// Install downloads the agent binary to options.Output, atomically, and makes it executable.
func Install(ctx context.Context, options Options) (string, error) {
	if options.Output == "" {
		return "", errors.New("an output path is required")
	}
	url, err := URL(options.URLTemplate, options.Version, options.GOOS, options.GOARCH)
	if err != nil {
		return "", err
	}
	client := options.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, response.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(options.Output), 0o755); err != nil { //nolint:gosec // A bin directory is world-readable.
		return "", err
	}
	temporary, err := os.CreateTemp(filepath.Dir(options.Output), ".aviato-agent-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temporary.Name())
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), response.Body); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if options.SHA256 != "" {
		if actual := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actual, options.SHA256) {
			return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", url, options.SHA256, actual)
		}
	}
	if err := os.Chmod(temporary.Name(), 0o755); err != nil { //nolint:gosec // The agent binary must be executable.
		return "", err
	}
	if err := os.Rename(temporary.Name(), options.Output); err != nil {
		return "", err
	}
	return url, nil
}
