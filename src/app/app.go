// Package app is the plugin's event router: it turns the host's events into
// calls on the calendar, the admin pages, the retention purge and the
// completion outbox, over the plugin-owned database (ABI 2 db_exec/db_query).
//
// It has no build tag, so the whole plugin runs natively in tests against the
// Go SDK's fake host; src/main.go (wasip1) only supplies the real
// collaborators. One event is one wasm instance: nothing is cached across
// events and the repository mutex only orders calls inside one event.
package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/universaltill/universal-till/sdk/plugin"

	"github.com/universaltill/ut-plugin-page-appointments/src/adapter"
	"github.com/universaltill/ut-plugin-page-appointments/src/admin"
	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/checkout"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

// PluginID is the manifest id; the tick event and the completed event are
// named after it (a test pins both to manifest.json).
const PluginID = "com.universaltill.page-appointments"

// TickEvent is the plugin's own schedule event (manifest schedules[]).
const TickEvent = PluginID + ".tick"

// OriginTill is stored on every row: the main till is the sole writer
// (ADR-0086), so the origin is one constant.
const OriginTill = "main"

// Event types the host sends.
const (
	evViewAsk   = "ui.view.ask"
	evActionAsk = "ui.action.ask"
)

// Publisher is event_publish (checkout.Publisher), so tests can fake it.
type Publisher = checkout.Publisher

// Deps are the host-facing collaborators.
type Deps struct {
	DB        store.DB
	Catalogue adapter.Catalogue
	Receipts  adapter.Receipts
	Pub       Publisher
	// ShopCurrency reads the till's currency (shop.context.v2); nil skips the
	// first-run seeding of the plugin's currency setting.
	ShopCurrency func() (string, error)
	// Records is the plugin-records channel for the replica snapshot; nil
	// disables it.
	Records Records
	// IDs returns unique ids; nil uses random UUIDv4 text.
	IDs engine.IDGen
	// Now overrides the event time (tests); zero uses the event's timestamp.
	Now time.Time
}

// Handlers returns the SDK handler table.
func Handlers(d Deps) plugin.Handlers {
	h := func(ev plugin.Event) (any, error) { return handle(d, ev) }
	return plugin.Handlers{evViewAsk: h, evActionAsk: h, TickEvent: h}
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// NewID is a random UUIDv4 (crypto/rand: WASI random_get in the guest).
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("appointments: no randomness: %v", err))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type askPayload struct {
	View     string             `json:"view"`
	Action   string             `json:"action"`
	Form     map[string]any     `json:"form"`
	Invalid  []string           `json:"invalid"`
	Params   map[string]string  `json:"params"`
	Locale   string             `json:"locale"`
	Operator *calendar.Operator `json:"operator"`
}

func eventTime(d Deps, ev plugin.Event) time.Time {
	if !d.Now.IsZero() {
		return d.Now.UTC()
	}
	if t, err := ev.Time(); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}

func handle(d Deps, ev plugin.Event) (any, error) {
	ctx := context.Background()
	now := eventTime(d, ev)
	ids := d.IDs
	if ids == nil {
		ids = NewID
	}
	repo := store.New(d.DB, fixedClock{now}, ids, OriginTill)
	co := &adapter.Checkout{Ctx: ctx, Repo: repo, StaffRef: staffRef(ctx, repo)}
	proc := &checkout.Processor{Store: co, Pub: d.Pub, Receipts: d.Receipts, Clock: fixedClock{now}, IDs: ids,
		Policy: policy(ctx, repo), StaffRef: co.StaffRef}

	switch ev.Type {
	case TickEvent:
		return nil, tick(ctx, repo, proc, d, now)
	case evViewAsk, evActionAsk:
	default:
		return nil, nil
	}
	var p askPayload
	if err := ev.Decode(&p); err != nil {
		return nil, fmt.Errorf("appointments: bad ask: %w", err)
	}
	if d.isReplica() {
		// A replica writes no appointment data and offers no action: every
		// page is the main till's read-only snapshot (design 7207 section 5).
		return replicaAnswer(d, p, now)
	}
	seedCurrency(ctx, repo, d)
	if isCalendar(p.View) {
		if ev.Type == evViewAsk {
			// Only the cheap, day-bounded part of the retention purge runs on
			// the interactive path (yesterday's walk-in queue, first names).
			// The scan of the appointment table belongs to the schedule tick
			// (hourly), so an open never pays for a large database (#7268).
			_ = purgeQueue(ctx, repo)
			_ = proc.Flush()
		}
		st := &adapter.Desk{Store: &adapter.Store{Ctx: ctx, Repo: repo, Catalogue: d.Catalogue, OriginTill: OriginTill},
			Proc: proc, Source: d.Receipts}
		deps := calendar.Deps{Store: st, IDs: ids, Route: "/plugin/appointments"}
		a := calendar.Ask{View: p.View, Form: p.Form, Params: p.Params, Locale: p.Locale, Operator: p.Operator, Now: now}
		if ev.Type == evActionAsk {
			a.Action = p.Action
		}
		ans, err := deps.Handle(a)
		if err != nil {
			return nil, err // never encode the empty answer of a failed ask
		}
		if ev.Type == evActionAsk {
			_ = publishSnapshot(ctx, repo, d, now) // never blocks the operator
		}
		return ans, nil
	}
	h := &admin.Handler{Repo: repo, NewID: ids, Prices: prices(d)}
	op := toAdminOperator(p.Operator)
	var (
		resp admin.Response
		err  error
	)
	defer func() { // a changed resource or service reaches replicas too
		if ev.Type == evActionAsk && err == nil {
			_ = publishSnapshot(ctx, repo, d, now)
		}
	}()
	if ev.Type == evViewAsk {
		resp, err = h.HandleView(ctx, admin.ViewRequest{View: p.View, Params: p.Params, Locale: p.Locale, Operator: op})
	} else {
		resp, err = h.HandleAction(ctx, admin.ActionRequest{View: p.View, Action: p.Action, Form: p.Form, Invalid: p.Invalid, Locale: p.Locale, Operator: op})
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func isCalendar(view string) bool {
	return view == calendar.ViewDay || view == calendar.ViewToday
}

func toAdminOperator(o *calendar.Operator) *admin.Operator {
	if o == nil {
		return nil
	}
	return &admin.Operator{UserID: o.UserID, Role: o.Role, Permissions: o.Permissions}
}

// tick is the schedule event: retention purge (names and the day's queue),
// the reconcile of any state projection a crash left behind, and the
// republish of unpublished completions.
func tick(ctx context.Context, repo *store.Repository, proc *checkout.Processor, d Deps, now time.Time) error {
	var errs []string
	if err := purge(ctx, repo); err != nil {
		errs = append(errs, "purge: "+err.Error())
	}
	if _, err := repo.Reconcile(ctx); err != nil {
		errs = append(errs, "reconcile: "+err.Error())
	}
	if err := proc.Flush(); err != nil {
		errs = append(errs, "flush: "+err.Error())
	}
	if err := publishSnapshot(ctx, repo, d, now); err != nil {
		errs = append(errs, "snapshot: "+err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("appointments tick: %s", strings.Join(errs, "; "))
	}
	return nil
}

func purge(ctx context.Context, repo *store.Repository) error {
	_, err := repo.Purge(ctx)
	return err
}

func purgeQueue(ctx context.Context, repo *store.Repository) error {
	day, err := repo.DayStart(ctx)
	if err != nil {
		return err
	}
	_, err = repo.PurgeQueue(ctx, day)
	return err
}

func policy(ctx context.Context, repo *store.Repository) engine.Policy {
	set, err := repo.Settings(ctx)
	if err != nil {
		return engine.DefaultPolicy()
	}
	return set.Policy
}

// staffRef maps a resource to its logbook staff reference, "" when unmapped.
func staffRef(ctx context.Context, repo *store.Repository) func(string) string {
	return func(id string) string {
		r, err := repo.Resource(ctx, id)
		if err != nil {
			return ""
		}
		return r.LogbookStaffRef
	}
}

// seedCurrency stores the till's currency as the plugin's default the first
// time, instead of the GB default of a shop that never opened the settings
// page (N15). A failure leaves the default; it never blocks the page.
func seedCurrency(ctx context.Context, repo *store.Repository, d Deps) {
	if d.ShopCurrency == nil {
		return
	}
	if stored, err := repo.SettingStored(ctx, "currency"); err != nil || stored {
		return
	}
	if code, err := d.ShopCurrency(); err == nil && len(code) == 3 {
		_ = repo.SeedCurrency(ctx, strings.ToUpper(code))
	}
}

func prices(d Deps) admin.PriceLookup {
	if d.Catalogue == nil {
		return nil
	}
	return func(sku string) (money.Money, bool) {
		items, err := d.Catalogue.Items([]string{sku})
		it, ok := items[sku]
		if err != nil || !ok {
			return 0, false
		}
		return it.Price, true
	}
}

// Marshal is a helper for tests: an answer as the host would read it.
func Marshal(a any) string { b, _ := json.Marshal(a); return string(b) }
