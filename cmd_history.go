package main

// The `flighttrack history` subcommand: show what is stored, or wipe it.

import (
	"flag"
	"fmt"
	"os"
	"time"

	"flighttrack/internal/history"
)

func runHistory(args []string) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	doClear := fs.Bool("clear", false, "delete the history file")
	if err := fs.Parse(args); err != nil {
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

	f, err := history.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}
	fmt.Println(path)
	if len(f.Entries) == 0 {
		fmt.Println("(empty)")
		return 0
	}

	now := time.Now()
	for _, e := range f.Entries {
		age, has := e.Age(now)
		switch {
		case !has:
			fmt.Printf("  %-22s no cached position\n", e.Label())
		case e.Usable(now):
			fmt.Printf("  %-22s %s old (%s, reusable)\n", e.Label(), shortAge(age), history.Rate(age))
		default:
			fmt.Printf("  %-22s %s old (stale, will refetch)\n", e.Label(), shortAge(age))
		}
	}
	return 0
}
