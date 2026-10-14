package connectorinstall_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/getaviato/aviato-go/internal/connectorinstall"
)

func TestURL(t *testing.T) {
	url, err := connectorinstall.URL("", "", "linux", "amd64")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/getaviato/aviato/releases/latest/download/aviato-connector-linux-x64", url)

	url, err = connectorinstall.URL("", "v1.2.0", "windows", "amd64")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/getaviato/aviato/releases/download/v1.2.0/aviato-connector-windows-x64.exe", url)

	url, err = connectorinstall.URL("https://mirror.example.com/{version}/{os}/{arch}/connector{ext}", "v1", "darwin", "arm64")
	require.NoError(t, err)
	assert.Equal(t, "https://mirror.example.com/v1/darwin/arm64/connector", url)

	_, err = connectorinstall.URL("", "", "darwin", "amd64")
	assert.Error(t, err, "unsupported platforms are reported")
}

func TestInstall(t *testing.T) {
	binary := []byte("#!/bin/sh\necho connector\n")
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		if r.URL.Path != "/v2/aviato-connector-linux-arm64" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(binary)
	}))
	t.Cleanup(server.Close)
	sum := sha256.Sum256(binary)
	output := filepath.Join(t.TempDir(), "bin", "aviato-connector")
	options := connectorinstall.Options{URLTemplate: server.URL + "/{version}/aviato-connector-{os}-{arch}{ext}", Version: "v2", GOOS: "linux", GOARCH: "arm64", Output: output, SHA256: hex.EncodeToString(sum[:])}

	_, err := connectorinstall.Install(context.Background(), options)
	require.NoError(t, err)
	assert.Equal(t, "/v2/aviato-connector-linux-arm64", requested)
	content, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, binary, content)
	info, err := os.Stat(output)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o100, "the binary is executable")

	options.SHA256 = "00"
	options.Output = filepath.Join(t.TempDir(), "other")
	_, err = connectorinstall.Install(context.Background(), options)
	require.ErrorContains(t, err, "checksum mismatch")
	assert.NoFileExists(t, options.Output)

	options.SHA256 = ""
	options.Version = "missing"
	_, err = connectorinstall.Install(context.Background(), options)
	require.ErrorContains(t, err, "HTTP 404")
}
