//go:build wasip1

// Appointments plugin entry point: a WASI command executed in-process by the
// till's wazero runtime. Slice 1 ships the scheduling engine only; this
// handler reads one event JSON from stdin, answers {} and exits 0. The engine
// is wired in a later slice.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	_ "time/tzdata" // a wasip1 guest has no system zoneinfo; the engine needs shop time zones
)

func main() {
	var ev map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&ev); err != nil {
		fmt.Fprintf(os.Stderr, "appointments: bad event: %v\n", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{})
}
