// Command aviato-agent-install downloads the Aviato agent binary for the current (or given)
// platform:
//
//	go run github.com/getaviato/aviato-go/cmd/aviato-agent-install@latest -out bin/aviato-agent
//
// The download URL is a template (-url or AVIATO_AGENT_URL_TEMPLATE) with the placeholders
// {release}, {version}, {os}, {arch} and {ext}, so the binary can come from a mirror.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"

	"github.com/getaviato/aviato-go/internal/agentinstall"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	defaultOutput := "bin/aviato-agent"
	if runtime.GOOS == "windows" {
		defaultOutput += ".exe"
	}
	var options agentinstall.Options
	flag.StringVar(&options.Version, "version", env("AVIATO_AGENT_VERSION", "latest"), "agent release tag, or latest (env AVIATO_AGENT_VERSION)")
	flag.StringVar(&options.URLTemplate, "url", env("AVIATO_AGENT_URL_TEMPLATE", agentinstall.DefaultURLTemplate), "download URL template (env AVIATO_AGENT_URL_TEMPLATE)")
	flag.StringVar(&options.Output, "out", defaultOutput, "path of the installed binary")
	flag.StringVar(&options.GOOS, "os", runtime.GOOS, "target operating system (Go name)")
	flag.StringVar(&options.GOARCH, "arch", runtime.GOARCH, "target architecture (Go name)")
	flag.StringVar(&options.SHA256, "sha256", env("AVIATO_AGENT_SHA256", ""), "expected SHA-256 of the binary (env AVIATO_AGENT_SHA256)")
	flag.Parse()

	if err := run(options); err != nil {
		fmt.Fprintln(os.Stderr, "aviato-agent-install:", err)
		os.Exit(1)
	}
}

func run(options agentinstall.Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	url, err := agentinstall.Install(ctx, options)
	if err != nil {
		return err
	}
	fmt.Printf("Installed %s from %s\n", options.Output, url)
	return nil
}
