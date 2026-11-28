package admin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

// Response is the answer to ui.view.ask or ui.action.ask: a document or a
// redirect (never both).
type Response struct {
	Document *Document `json:"document,omitempty"`
	Redirect string    `json:"redirect,omitempty"`
}

// Operator is the signed-in operator the till sends with every ask.
type Operator struct {
	UserID      string   `json:"user_id"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

// ViewRequest is the ui.view.ask payload.
type ViewRequest struct {
	View     string            `json:"view"`
	Params   map[string]string `json:"params"`
	Locale   string            `json:"locale"`
	Operator *Operator         `json:"operator"`
}

// ActionRequest is the ui.action.ask payload. Form values are strings,
// booleans or {"minor","currency"} objects; Invalid lists unreadable fields.
type ActionRequest struct {
	View     string         `json:"view"`
	Action   string         `json:"action"`
	Form     map[string]any `json:"form"`
	Invalid  []string       `json:"invalid"`
	Locale   string         `json:"locale"`
	Operator *Operator      `json:"operator"`
}

// PriceLookup returns the catalogue price of a service SKU (a service entity
// carries no price; it is read live from the catalogue).
type PriceLookup func(sku string) (money.Money, bool)

// Handler answers the admin views and actions.
type Handler struct {
	Repo   *store.Repository
	NewID  engine.IDGen
	Prices PriceLookup // optional; nil shows no price
	// CanAdmin decides whether the operator may use the admin pages. When it
	// is nil the handler fails closed with DefaultCanAdmin: no operator
	// (sign-in off) or a manager, admin or owner role.
	CanAdmin func(*Operator) bool
}

// Error codes shown to the operator; each has a key
// appointments.admin.error.<code> in locales/en.json.
var errorCodes = map[error]string{
	store.ErrInvalidInput:   "invalid_input",
	store.ErrNameChars:      "name_chars",
	store.ErrNameTooLong:    "name_too_long",
	store.ErrTimeOffReason:  "time_off_reason",
	store.ErrRetentionRange: "retention_range",
	store.ErrNotFound:       "not_found",
	engine.ErrInvalidPolicy: "invalid_policy",
}

// ErrorKey is the locale key for an error.
func ErrorKey(err error) string {
	for e, code := range errorCodes {
		if errors.Is(err, e) {
			return "appointments.admin.error." + code
		}
	}
	return "appointments.admin.error.generic"
}

// ErrorKeys lists every error locale key (for the locale test).
func ErrorKeys() []string {
	out := []string{"appointments.admin.error.generic", "appointments.admin.error.form", "appointments.admin.error.unknown", "appointments.admin.error.forbidden"}
	for _, c := range errorCodes {
		out = append(out, "appointments.admin.error."+c)
	}
	sort.Strings(out)
	return out
}

// DefaultCanAdmin is the check used when CanAdmin is unset, the same rule as
// calendar.Operator.IsManager: a missing operator (the till sends none when
// sign-in is off) or the role manager, admin or owner.
func DefaultCanAdmin(op *Operator) bool {
	if op == nil {
		return true
	}
	switch op.Role {
	case "manager", "admin", "owner":
		return true
	}
	return false
}

func (h *Handler) allowed(op *Operator) bool {
	if h.CanAdmin == nil {
		return DefaultCanAdmin(op)
	}
	return h.CanAdmin(op)
}

// HandleView answers a GET.
func (h *Handler) HandleView(ctx context.Context, req ViewRequest) (Response, error) {
	if !h.allowed(req.Operator) {
		return docResp(errDoc("appointments.admin.error.forbidden")), nil
	}
	doc, err := h.build(ctx, req.View, req.Locale, req.Params["resource"], nil)
	if err != nil {
		return Response{}, err
	}
	return docResp(doc), nil
}

// HandleAction answers a POST: it applies the action and answers the page
// again with a "saved" notice, or the same page with an error notice and the
// reason. Unknown views, actions or unreadable fields are answered as an
// error document, never as a half-applied change.
func (h *Handler) HandleAction(ctx context.Context, req ActionRequest) (Response, error) {
	if !h.allowed(req.Operator) {
		return docResp(errDoc("appointments.admin.error.forbidden")), nil
	}
	f := formValues{m: req.Form}
	var (
		err      error
		resource string
	)
	if len(req.Invalid) > 0 {
		return h.answer(ctx, req.View, req.Locale, "", errors.New("form"), "appointments.admin.error.form")
	}
	switch req.Action {
	case ActSaveResource:
		resource, err = h.saveResource(ctx, f)
	case ActShowResource:
		resource = f.str("resource")
	case ActSaveService:
		err = h.saveService(ctx, f)
	case ActShowHours:
		resource = f.str("resource")
	case ActAddHours:
		resource = f.str("resource")
		err = h.addHours(ctx, f)
	case ActClearHours:
		resource = f.str("resource")
		err = h.clearHours(ctx, f)
	case ActAddTimeOff:
		err = h.addTimeOff(ctx, f)
	case ActRemoveTimeOff:
		err = h.Repo.DeleteTimeOff(ctx, f.str("entry"))
	case ActSaveSettings:
		err = h.saveSettings(ctx, f)
	default:
		return h.answer(ctx, req.View, req.Locale, "", errors.New("unknown"), "appointments.admin.error.unknown")
	}
	if err != nil {
		return h.answer(ctx, req.View, req.Locale, resource, err, ErrorKey(err))
	}
	ok := notice("info", "appointments.admin.saved")
	doc, berr := h.build(ctx, req.View, req.Locale, resource, &ok)
	if berr != nil {
		return Response{}, berr
	}
	return docResp(doc), nil
}

func (h *Handler) answer(ctx context.Context, view, locale, resource string, _ error, key string) (Response, error) {
	n := notice("error", key)
	doc, err := h.build(ctx, view, locale, resource, &n)
	if err != nil {
		return Response{}, err
	}
	return docResp(doc), nil
}

func docResp(d Document) Response { return Response{Document: &d} }

func errDoc(key string) Document {
	return Document{Version: 1, Components: []Component{notice("error", key)}}
}

// ---- form parsing ---------------------------------------------------------

type formValues struct{ m map[string]any }

func (f formValues) str(k string) string {
	s, _ := f.m[k].(string)
	return strings.TrimSpace(s)
}

func (f formValues) flag(k string) bool { b, _ := f.m[k].(bool); return b }

// intv reads a number field: a decimal string without fraction ("1.0" ok).
func (f formValues) intv(k string) (int, error) {
	s := f.str(k)
	if s == "" {
		return 0, store.ErrInvalidInput
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v != float64(int(v)) || v < -1e9 || v > 1e9 {
		return 0, store.ErrInvalidInput
	}
	return int(v), nil
}

var hhmm = regexp.MustCompile(`^([0-9]{1,2}):([0-9]{2})$`)

// parseClock reads "HH:MM" (24:00 allowed as the end of day) as minutes.
func parseClock(s string) (int, error) {
	m := hhmm.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, store.ErrInvalidInput
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	if mi > 59 || h > 24 || (h == 24 && mi != 0) {
		return 0, store.ErrInvalidInput
	}
	return h*60 + mi, nil
}

const dtLayout = "2006-01-02 15:04"

func parseLocal(loc *time.Location, s string) (time.Time, error) {
	t, err := time.ParseInLocation(dtLayout, strings.TrimSpace(s), loc)
	if err != nil {
		return time.Time{}, store.ErrInvalidInput
	}
	return t.UTC(), nil
}

var skuPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ---- actions ----------------------------------------------------------------

const newResource = "_new"

// saveResource writes the resource and returns its id (the page then shows
// it pre-filled). A field the form does not carry keeps the stored value, so
// a partial post never wipes the logbook link, colour or kind (review N3).
func (h *Handler) saveResource(ctx context.Context, f formValues) (string, error) {
	id := f.str("resource")
	rec := store.ResourceRecord{Resource: engine.Resource{Kind: "staff", Active: true}}
	if id == "" || id == newResource {
		id = h.NewID()
	} else {
		cur, err := h.Repo.Resource(ctx, id)
		if err != nil {
			return "", err
		}
		rec = cur
	}
	rec.ID = id
	rec.DisplayName = f.str("name")
	if _, ok := f.m["kind"]; ok {
		rec.Kind = f.str("kind")
	}
	if _, ok := f.m["status"]; ok {
		rec.Active = f.str("status") != "inactive"
	}
	if _, ok := f.m["colour"]; ok {
		rec.Colour = f.str("colour")
	}
	if _, ok := f.m["logbook_staff_ref"]; ok {
		rec.LogbookStaffRef = f.str("logbook_staff_ref")
	}
	return id, h.Repo.SaveResource(ctx, rec)
}

func (h *Handler) saveService(ctx context.Context, f formValues) error {
	sku := f.str("sku")
	if !skuPattern.MatchString(sku) {
		return store.ErrInvalidInput
	}
	dur, err := f.intv("duration_min")
	if err != nil {
		return err
	}
	buf, err := f.intv("buffer_min")
	if err != nil {
		return err
	}
	svc := engine.Service{SKU: sku, DurationMin: dur, BufferMin: buf, All: f.flag("all_resources"), Active: f.flag("active")}
	if !svc.All {
		rs, err := h.Repo.Resources(ctx)
		if err != nil {
			return err
		}
		for _, r := range resourceToggles(rs) {
			if f.flag(r.field) {
				svc.ResourceIDs = append(svc.ResourceIDs, r.id)
			}
		}
	}
	return h.Repo.SaveService(ctx, svc)
}

func parseWeekday(f formValues) (time.Weekday, error) {
	n, err := f.intv("weekday")
	if err != nil || n < 0 || n > 6 {
		return 0, store.ErrInvalidInput
	}
	return time.Weekday(n), nil
}

func (h *Handler) addHours(ctx context.Context, f formValues) error {
	rec, err := h.Repo.Resource(ctx, f.str("resource"))
	if err != nil {
		return err
	}
	d, err := parseWeekday(f)
	if err != nil {
		return err
	}
	start, err := parseClock(f.str("start"))
	if err != nil {
		return err
	}
	end, err := parseClock(f.str("end"))
	if err != nil {
		return err
	}
	hours := rec.WorkingHours
	hours[d] = append(append([]engine.Window(nil), hours[d]...), engine.Window{StartMin: start, EndMin: end})
	return h.Repo.SetWorkingHours(ctx, rec.ID, hours)
}

func (h *Handler) clearHours(ctx context.Context, f formValues) error {
	rec, err := h.Repo.Resource(ctx, f.str("resource"))
	if err != nil {
		return err
	}
	d, err := parseWeekday(f)
	if err != nil {
		return err
	}
	delete(rec.WorkingHours, d)
	return h.Repo.SetWorkingHours(ctx, rec.ID, rec.WorkingHours)
}

func (h *Handler) addTimeOff(ctx context.Context, f formValues) error {
	set, err := h.Repo.Settings(ctx)
	if err != nil {
		return err
	}
	loc, err := set.Location()
	if err != nil {
		return err
	}
	from, err := parseLocal(loc, f.str("from"))
	if err != nil {
		return err
	}
	to, err := parseLocal(loc, f.str("to"))
	if err != nil {
		return err
	}
	if _, err := h.Repo.Resource(ctx, f.str("resource")); err != nil {
		return err
	}
	_, err = h.Repo.AddTimeOff(ctx, f.str("resource"), from, to, f.str("reason"))
	return err
}

func (h *Handler) saveSettings(ctx context.Context, f formValues) error {
	cur, err := h.Repo.Settings(ctx)
	if err != nil {
		return err
	}
	for _, p := range []struct {
		field string
		dst   *int
	}{
		{"slot_step_min", &cur.SlotStepMin}, {"no_show_grace_min", &cur.NoShowGraceMin},
		{"late_cancel_hours", &cur.LateCancelHours}, {"overrun_min", &cur.OverrunMin}, {"retention_days", &cur.RetentionDays},
	} {
		n, err := f.intv(p.field)
		if err != nil {
			return err
		}
		*p.dst = n
	}
	cur.AllowDoubleBooking = f.flag("allow_double_booking")
	cur.ShopZone = f.str("shop_zone")
	cur.Currency = strings.ToUpper(f.str("currency"))
	return h.Repo.SaveSettings(ctx, cur)
}

// ---- per-resource toggle names ------------------------------------------------

var unsafeName = regexp.MustCompile(`[^a-z0-9_]`)

type toggle struct{ id, field, label string }

// resourceToggles names one toggle per resource: res_<sanitised id>, unique.
// The name is derived from the id, so a resource added between the page and
// the post cannot shift another resource's toggle.
func resourceToggles(rs []store.ResourceRecord) []toggle {
	seen := map[string]bool{}
	out := make([]toggle, 0, len(rs))
	for i, r := range rs {
		n := "res_" + unsafeName.ReplaceAllString(strings.ToLower(r.ID), "_")
		if len(n) > 56 {
			n = n[:56]
		}
		if seen[n] {
			n = fmt.Sprintf("%s_%d", n, i)
		}
		seen[n] = true
		out = append(out, toggle{id: r.ID, field: n, label: r.DisplayName})
	}
	return out
}
