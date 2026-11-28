//go:build wasip1

// Appointments plugin entry point: a WASI command executed in-process by the
// till's wazero runtime, one event per run. The router and every rule live in
// src/app (built and tested natively); this file only plugs in the host
// functions: db_exec/db_query (plugin-owned SQLite, ABI 2), view_query and
// event_publish.
package main

import (
	"encoding/json"

	"github.com/universaltill/universal-till/sdk/plugin"
	_ "time/tzdata" // a wasip1 guest has no system zoneinfo; the engine needs shop time zones

	"github.com/universaltill/ut-plugin-page-appointments/src/app"
	"github.com/universaltill/ut-plugin-page-appointments/src/hostdb"
	"github.com/universaltill/ut-plugin-page-appointments/src/hostview"
)

// eventPub is event_publish: 0 when queued, -1 for any refusal (the outbox
// keeps the payload and republishes on a later event).
type eventPub struct{}

func (eventPub) Publish(name string, payload []byte) int {
	if err := plugin.EventPublish(name, json.RawMessage(payload)); err != nil {
		return -1
	}
	return 0
}

func main() {
	plugin.Run(app.Handlers(app.Deps{
		DB:           hostdb.New(),
		Catalogue:    hostview.Catalogue{},
		Receipts:     hostview.Receipts{},
		Pub:          eventPub{},
		ShopCurrency: hostview.ShopCurrency,
	}))
}
