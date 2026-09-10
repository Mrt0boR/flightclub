package main

// The `flighttrack history` subcommand: show what is stored, or wipe it.

import (
	"flag"
	"fmt"
	"os"
	"time"

	"flighttrack/internal/history"
	"flighttrack/internal/textfmt"
)

func runHistory(args []string) int {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	doClear := flags.Bool("clear", false, "delete the history file")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	path, err := history.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	if *doClear {
		if err := history.Clear(path); err != nil {
			fmt.Fprintf(os.Stderr, "could not clear history: %v\n", err)
			return 1
		}
		fmt.Printf("history cleared (%s)\n", path)
		return 0
	}

	saved, err := history.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}
	fmt.Println(path)
	if len(saved.Entries) == 0 {
		fmt.Println("(empty)")
		return 0
	}

	now := time.Now()
	for _, entry := range saved.Entries {
		age, hasPosition := entry.Age(now)
		switch {
		case !hasPosition:
			fmt.Printf("  %-22s no cached position\n", entry.Label())
		case entry.Usable(now):
			fmt.Printf("  %-22s %s old (%s, reusable)\n", entry.Label(), textfmt.ShortAge(age), history.Rate(age))
		default:
			fmt.Printf("  %-22s %s old (stale, will refetch)\n", entry.Label(), textfmt.ShortAge(age))
		}
	}
	return 0
}
