package main

// The `flighttrack version` subcommand: which build this is, and whether a
// newer one has been released.

import (
	"context"
	"fmt"
	"time"

	"flighttrack/internal/version"
)

func runVersion() int {
	fmt.Printf("flighttrack %s\n", version.Version)

	if version.IsDev() {
		fmt.Println("built from source; update checks are off for unstamped builds")
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	path, err := version.DefaultCachePath()
	if err != nil {
		return 0 // nothing useful to say, and nothing the user can do
	}
	release, available := version.Check(ctx, path)
	if !available {
		fmt.Println("up to date")
		return 0
	}
	fmt.Printf("\nupdate available: %s\n", release.Version)
	if release.URL != "" {
		fmt.Println(release.URL)
	}
	fmt.Println("\nto update:  .\\install.ps1 -Update")
	return 0
}
