// Command flighttrack is a terminal flight tracker built on the OpenSky
// Network live states API.
//
// The dashboard takes the flight number from your ticket, finds the aircraft,
// works out an arrival time, and counts down against your system clock rather
// than hammering the API. Watch mode does the same job with no interface,
// reporting only takeoffs and landings.
//
//	flighttrack                                   # dashboard
//	flighttrack -flight QF2 -from SYD -to LHR     # dashboard, preseeded
//	flighttrack watch -flights QF2,CX251          # background
//	flighttrack history                           # saved searches
//
// Credentials, if you have them, come from the environment:
//
//	OPENSKY_CLIENT_ID / OPENSKY_CLIENT_SECRET
//
// This package is only the command line. The work lives in:
//
//	internal/ui        the dashboard
//	internal/watch     background watch mode
//	internal/opensky   the API client and flight-number matching
//	internal/eta       arrival estimates
//	internal/airports  the embedded airport table
//	internal/history   saved searches and the position cache
//	internal/notify    desktop and webhook delivery
//	internal/textfmt   shared text helpers
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"flighttrack/internal/ui"
	"flighttrack/internal/watch"
)

func main() {
	// One binary, several modes. A bare invocation opens the dashboard, which
	// is what most runs want, so subcommands are only checked for explicitly.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "watch":
			os.Exit(watch.Run(os.Args[2:]))
		case "history":
			os.Exit(runHistory(os.Args[2:]))
		case "help", "--help":
			registerDashFlags() // so the option list is populated
			usage()
			return
		}
	}
	runDashboard()
}

func usage() {
	fmt.Fprintln(os.Stderr, `flighttrack - follow flights using the OpenSky Network

Usage:
  flighttrack [options]              open the dashboard (default)
  flighttrack watch [options]        follow flights in the background, no interface
  flighttrack history [-clear]       show or wipe saved searches
  flighttrack help                   this message

Dashboard options:`)
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, `
Examples:
  flighttrack
  flighttrack -flight QF2 -to LHR
  flighttrack watch -flights QF2,CX251 -notify console,desktop
  flighttrack watch -help

Credentials, if you have them, come from the environment:
  OPENSKY_CLIENT_ID / OPENSKY_CLIENT_SECRET`)
}

// dashFlags holds the dashboard's options. Registering them is split out from
// parsing so `flighttrack help` can list them without starting the dashboard.
type dashFlags struct {
	flight  *string
	origin  *string
	dest    *string
	refresh *time.Duration
	hook    *string
	noAuto  *bool
}

func registerDashFlags() dashFlags {
	flag.Usage = usage
	return dashFlags{
		flight:  flag.String("flight", "", "flight number to start on, e.g. BA117"),
		origin:  flag.String("from", "", "origin airport code, e.g. DUB (optional, enables the route progress bar)"),
		dest:    flag.String("to", "", "destination airport code, e.g. JFK"),
		refresh: flag.Duration("refresh", 5*time.Minute, "how often to auto-refresh the snapshot"),
		hook:    flag.String("webhook", os.Getenv("FLIGHTTRACK_WEBHOOK"), "optional https URL to POST events to"),
		noAuto:  flag.Bool("no-auto-refresh", false, "start with auto-refresh disabled"),
	}
}

func runDashboard() {
	flags := registerDashFlags()
	flag.Parse()

	if *flags.refresh < time.Minute {
		fmt.Fprintln(os.Stderr, "refresh interval must be at least 1m, to stay inside the free API quota")
		os.Exit(2)
	}

	err := ui.Run(ui.Config{
		Flight:      *flags.flight,
		Origin:      *flags.origin,
		Dest:        *flags.dest,
		Refresh:     *flags.refresh,
		AutoRefresh: !*flags.noAuto,
		WebhookURL:  *flags.hook,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "flighttrack: %v\n", err)
		os.Exit(1)
	}
}
