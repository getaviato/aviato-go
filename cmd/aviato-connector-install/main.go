// Command aviato-connector-install downloads the Aviato connector binary for the current (or given)
// platform:
//
//	go run github.com/getaviato/aviato-go/cmd/aviato-connector-install@latest -out bin/aviato-connector
//
// The download URL is a template (-url or AVIATO_CONNECTOR_URL_TEMPLATE) with the placeholders
// {release}, {version}, {os}, {arch} and {ext}, so the binary can come from a mirror.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"github.com/getaviato/aviato-go/internal/connectorinstall"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	if legacy := strings.Replace(name, "AVIATO_CONNECTOR_", "AVIATO_AGENT_", 1); legacy != name {
		if value := os.Getenv(legacy); value != "" {
			return value
		}
	}
	return fallback
}

func main() {
	defaultOutput := "bin/aviato-connector"
	if runtime.GOOS == "windows" {
		defaultOutput += ".exe"
	}
	var options connectorinstall.Options
	flag.StringVar(&options.Version, "version", env("AVIATO_CONNECTOR_VERSION", "latest"), "connector release tag, or latest (env AVIATO_CONNECTOR_VERSION)")
	flag.StringVar(&options.URLTemplate, "url", env("AVIATO_CONNECTOR_URL_TEMPLATE", connectorinstall.DefaultURLTemplate), "download URL template (env AVIATO_CONNECTOR_URL_TEMPLATE)")
	flag.StringVar(&options.Output, "out", defaultOutput, "path of the installed binary")
	flag.StringVar(&options.GOOS, "os", runtime.GOOS, "target operating system (Go name)")
	flag.StringVar(&options.GOARCH, "arch", runtime.GOARCH, "target architecture (Go name)")
	flag.StringVar(&options.SHA256, "sha256", env("AVIATO_CONNECTOR_SHA256", ""), "expected SHA-256 of the binary (env AVIATO_CONNECTOR_SHA256)")
	flag.Parse()

	if err := run(options); err != nil {
		fmt.Fprintln(os.Stderr, "aviato-connector-install:", err)
		os.Exit(1)
	}
}

func run(options connectorinstall.Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	url, err := connectorinstall.Install(ctx, options)
	if err != nil {
		return err
	}
	fmt.Printf("Installed %s from %s\n", options.Output, url)
	return nil
}
