package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

func (h *Handler) build(ctx context.Context, view, resource string, n *Notice) (Document, error) {
	var (
		doc Document
		err error
	)
	switch view {
	case ViewResources:
		doc, err = h.resourcesDoc(ctx)
	case ViewServices:
		doc, err = h.servicesDoc(ctx)
	case ViewHours:
		doc, err = h.hoursDoc(ctx, resource)
	case ViewTimeOff:
		doc, err = h.timeOffDoc(ctx)
	case ViewSettings:
		doc, err = h.settingsDoc(ctx)
	default:
		return errDoc("appointments.admin.error.unknown"), nil
	}
	if err != nil {
		return Document{}, err
	}
	if n != nil {
		doc.Components = append([]Component{*n}, doc.Components...)
	}
	return doc, nil
}

func newDoc(titleKey string, cs ...Component) Document {
	t := K(titleKey)
	return Document{Version: 1, Title: &t, Components: cs}
}

func yesNo(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func resourceOptions(rs []store.ResourceRecord, withNew bool) []Option {
	var out []Option
	if withNew {
		out = append(out, Option{Value: newResource, Label: K("appointments.admin.resources.new")})
	}
	for _, r := range rs {
		out = append(out, Option{Value: r.ID, Label: L(r.DisplayName)})
	}
	return out
}

func (h *Handler) resourcesDoc(ctx context.Context) (Document, error) {
	rs, err := h.Repo.Resources(ctx)
	if err != nil {
		return Document{}, err
	}
	cs := []Component{paragraph("appointments.admin.resources.intro")}
	if len(rs) == 0 {
		b := K("appointments.admin.resources.empty.body")
		cs = append(cs, EmptyState{Type: "empty_state", Title: K("appointments.admin.resources.empty.title"), Body: &b})
	} else {
		rows := make([][]any, len(rs))
		for i, r := range rs {
			rows[i] = []any{r.DisplayName, r.Kind, yesNo(r.Active, "active", "inactive"), r.LogbookStaffRef}
		}
		cs = append(cs, table([]Column{
			{K("appointments.admin.resources.col.name"), "text"}, {K("appointments.admin.resources.col.kind"), "text"},
			{K("appointments.admin.resources.col.status"), "text"}, {K("appointments.admin.resources.col.logbook"), "text"},
		}, rows))
	}
	cs = append(cs, heading("appointments.admin.resources.form"), paragraph("appointments.admin.resources.logbook_hint"),
		form(ActSaveResource, "appointments.admin.save",
			Field{Name: "resource", Label: K("appointments.admin.resources.field.resource"), Kind: "select", Options: resourceOptions(rs, true), Value: newResource},
			Field{Name: "name", Label: K("appointments.admin.resources.field.name"), Kind: "text", Required: true},
			Field{Name: "kind", Label: K("appointments.admin.resources.field.kind"), Kind: "select", Value: "staff", Options: []Option{
				{"staff", K("appointments.admin.resources.kind.staff")}, {"chair", K("appointments.admin.resources.kind.chair")}}},
			Field{Name: "colour", Label: K("appointments.admin.resources.field.colour"), Kind: "text"},
			Field{Name: "status", Label: K("appointments.admin.resources.field.status"), Kind: "select", Value: "active", Options: []Option{
				{"active", K("appointments.admin.resources.status.active")}, {"inactive", K("appointments.admin.resources.status.inactive")}}},
			Field{Name: "logbook_staff_ref", Label: K("appointments.admin.resources.field.logbook"), Kind: "text"},
		))
	return newDoc(kTitleResources, cs...), nil
}

func (h *Handler) servicesDoc(ctx context.Context) (Document, error) {
	svcs, err := h.Repo.Services(ctx)
	if err != nil {
		return Document{}, err
	}
	rs, err := h.Repo.Resources(ctx)
	if err != nil {
		return Document{}, err
	}
	set, err := h.Repo.Settings(ctx)
	if err != nil {
		return Document{}, err
	}
	names := map[string]string{}
	for _, r := range rs {
		names[r.ID] = r.DisplayName
	}
	cs := []Component{paragraph("appointments.admin.services.intro")}
	if len(svcs) == 0 {
		cs = append(cs, EmptyState{Type: "empty_state", Title: K("appointments.admin.services.empty.title")})
	} else {
		rows := make([][]any, len(svcs))
		for i, s := range svcs {
			who := "*"
			if !s.All {
				var ns []string
				for _, id := range s.ResourceIDs {
					ns = append(ns, names[id])
				}
				who = strings.Join(ns, ", ")
			}
			var price any
			if h.Prices != nil {
				if p, ok := h.Prices(s.SKU); ok {
					price = Money(p, set.Currency)
				}
			}
			rows[i] = []any{s.SKU, s.DurationMin, s.BufferMin, who, yesNo(s.Active, "active", "inactive"), price}
		}
		cs = append(cs, table([]Column{
			{K("appointments.admin.services.col.sku"), "text"}, {K("appointments.admin.services.col.duration"), "number"},
			{K("appointments.admin.services.col.buffer"), "number"}, {K("appointments.admin.services.col.resources"), "text"},
			{K("appointments.admin.services.col.status"), "text"}, {K("appointments.admin.services.col.price"), "money"},
		}, rows))
	}
	fields := []Field{
		{Name: "sku", Label: K("appointments.admin.services.field.sku"), Kind: "text", Required: true},
		{Name: "duration_min", Label: K("appointments.admin.services.field.duration"), Kind: "number", Required: true, Value: "30"},
		{Name: "buffer_min", Label: K("appointments.admin.services.field.buffer"), Kind: "number", Required: true, Value: "0"},
		{Name: "active", Label: K("appointments.admin.services.field.active"), Kind: "toggle", Value: true},
		{Name: "all_resources", Label: K("appointments.admin.services.field.all"), Kind: "toggle", Value: true},
	}
	for _, t := range resourceToggles(rs) {
		fields = append(fields, Field{Name: t.field, Label: L(t.label), Kind: "toggle"})
	}
	cs = append(cs, heading("appointments.admin.services.form"), paragraph("appointments.admin.services.price_hint"),
		form(ActSaveService, "appointments.admin.save", fields...))
	return newDoc(kTitleServices, cs...), nil
}

func weekdayKey(d time.Weekday) string { return "appointments.admin.weekday." + strconv.Itoa(int(d)) }

func clock(min int) string { return fmt.Sprintf("%02d:%02d", min/60, min%60) }

func (h *Handler) hoursDoc(ctx context.Context, resourceID string) (Document, error) {
	rs, err := h.Repo.Resources(ctx)
	if err != nil {
		return Document{}, err
	}
	cs := []Component{paragraph("appointments.admin.hours.intro")}
	if len(rs) == 0 {
		cs = append(cs, EmptyState{Type: "empty_state", Title: K("appointments.admin.hours.no_resources")})
		return newDoc(kTitleHours, cs...), nil
	}
	cur := rs[0]
	for _, r := range rs {
		if r.ID == resourceID {
			cur = r
		}
	}
	pick := form(ActShowHours, "appointments.admin.hours.show",
		Field{Name: "resource", Label: K("appointments.admin.hours.field.resource"), Kind: "select", Options: resourceOptions(rs, false), Value: cur.ID})
	cs = append(cs, pick, Paragraph{Type: "text", Text: L(cur.DisplayName)})
	have := false
	for d := time.Monday; d < time.Monday+7; d++ { // Monday first
		wd := d % 7
		ws := append([]engine.Window(nil), cur.WorkingHours[wd]...)
		if len(ws) == 0 {
			continue
		}
		have = true
		cs = append(cs, heading(weekdayKey(wd)))
		rows := make([][]any, len(ws))
		for i, w := range ws {
			rows[i] = []any{clock(w.StartMin), clock(w.EndMin)}
		}
		cs = append(cs, table([]Column{{K("appointments.admin.hours.col.from"), "text"}, {K("appointments.admin.hours.col.to"), "text"}}, rows))
	}
	if !have {
		cs = append(cs, EmptyState{Type: "empty_state", Title: K("appointments.admin.hours.empty")})
	}
	wdOpts := make([]Option, 7)
	for i := range wdOpts {
		d := time.Weekday((i + 1) % 7) // Monday first
		wdOpts[i] = Option{Value: strconv.Itoa(int(d)), Label: K(weekdayKey(d))}
	}
	cs = append(cs, heading("appointments.admin.hours.add"), form(ActAddHours, "appointments.admin.hours.add_submit",
		Field{Name: "resource", Label: K("appointments.admin.hours.field.resource"), Kind: "select", Options: resourceOptions(rs, false), Value: cur.ID},
		Field{Name: "weekday", Label: K("appointments.admin.hours.field.weekday"), Kind: "select", Options: wdOpts, Value: "1"},
		Field{Name: "start", Label: K("appointments.admin.hours.field.start"), Kind: "text", Required: true, Value: "09:00"},
		Field{Name: "end", Label: K("appointments.admin.hours.field.end"), Kind: "text", Required: true, Value: "17:00"},
	), heading("appointments.admin.hours.clear"), form(ActClearHours, "appointments.admin.hours.clear_submit",
		Field{Name: "resource", Label: K("appointments.admin.hours.field.resource"), Kind: "select", Options: resourceOptions(rs, false), Value: cur.ID},
		Field{Name: "weekday", Label: K("appointments.admin.hours.field.weekday"), Kind: "select", Options: wdOpts, Value: "1"},
	))
	return newDoc(kTitleHours, cs...), nil
}

func (h *Handler) timeOffDoc(ctx context.Context) (Document, error) {
	rs, err := h.Repo.Resources(ctx)
	if err != nil {
		return Document{}, err
	}
	tos, err := h.Repo.TimeOff(ctx, "")
	if err != nil {
		return Document{}, err
	}
	set, err := h.Repo.Settings(ctx)
	if err != nil {
		return Document{}, err
	}
	loc, err := set.Location()
	if err != nil {
		return Document{}, err
	}
	names := map[string]string{}
	for _, r := range rs {
		names[r.ID] = r.DisplayName
	}
	cs := []Component{paragraph("appointments.admin.timeoff.intro")}
	if len(tos) == 0 {
		cs = append(cs, EmptyState{Type: "empty_state", Title: K("appointments.admin.timeoff.empty")})
	} else {
		rows := make([][]any, len(tos))
		opts := make([]Option, len(tos))
		for i, t := range tos {
			rows[i] = []any{names[t.ResourceID], t.From.In(loc).Format(dtLayout), t.To.In(loc).Format(dtLayout), t.Reason}
			opts[i] = Option{Value: t.ID, Label: L(names[t.ResourceID] + " " + t.From.In(loc).Format(dtLayout))}
		}
		cs = append(cs, table([]Column{
			{K("appointments.admin.timeoff.col.resource"), "text"}, {K("appointments.admin.timeoff.col.from"), "text"},
			{K("appointments.admin.timeoff.col.to"), "text"}, {K("appointments.admin.timeoff.col.reason"), "text"},
		}, rows), heading("appointments.admin.timeoff.remove"), form(ActRemoveTimeOff, "appointments.admin.timeoff.remove_submit",
			Field{Name: "entry", Label: K("appointments.admin.timeoff.field.entry"), Kind: "select", Options: opts, Required: true}))
	}
	if len(rs) > 0 {
		reasons := make([]Option, len(store.TimeOffReasons))
		for i, r := range store.TimeOffReasons {
			reasons[i] = Option{Value: r, Label: K("appointments.admin.timeoff.reason." + r)}
		}
		cs = append(cs, heading("appointments.admin.timeoff.add"), paragraph("appointments.admin.timeoff.format_hint"),
			form(ActAddTimeOff, "appointments.admin.timeoff.add_submit",
				Field{Name: "resource", Label: K("appointments.admin.timeoff.field.resource"), Kind: "select", Options: resourceOptions(rs, false), Required: true},
				Field{Name: "from", Label: K("appointments.admin.timeoff.field.from"), Kind: "text", Required: true},
				Field{Name: "to", Label: K("appointments.admin.timeoff.field.to"), Kind: "text", Required: true},
				Field{Name: "reason", Label: K("appointments.admin.timeoff.field.reason"), Kind: "select", Options: reasons, Value: "other"},
			))
	}
	return newDoc(kTitleTimeOff, cs...), nil
}

func (h *Handler) settingsDoc(ctx context.Context) (Document, error) {
	s, err := h.Repo.Settings(ctx)
	if err != nil {
		return Document{}, err
	}
	steps := []int{5, 10, 15, 20, 30, 60}
	opts := make([]Option, len(steps))
	for i, v := range steps {
		opts[i] = Option{Value: strconv.Itoa(v), Label: L(strconv.Itoa(v))}
	}
	num := func(n int) string { return strconv.Itoa(n) }
	return newDoc(kTitleSettings,
		paragraph("appointments.admin.settings.intro"),
		paragraph("appointments.admin.settings.privacy"),
		form(ActSaveSettings, "appointments.admin.save",
			Field{Name: "slot_step_min", Label: K("appointments.admin.settings.field.slot_step"), Kind: "select", Options: opts, Value: num(s.SlotStepMin)},
			Field{Name: "no_show_grace_min", Label: K("appointments.admin.settings.field.no_show_grace"), Kind: "number", Required: true, Value: num(s.NoShowGraceMin)},
			Field{Name: "late_cancel_hours", Label: K("appointments.admin.settings.field.late_cancel"), Kind: "number", Required: true, Value: num(s.LateCancelHours)},
			Field{Name: "overrun_min", Label: K("appointments.admin.settings.field.overrun"), Kind: "number", Required: true, Value: num(s.OverrunMin)},
			Field{Name: "allow_double_booking", Label: K("appointments.admin.settings.field.double_booking"), Kind: "toggle", Value: s.AllowDoubleBooking},
			Field{Name: "retention_days", Label: K("appointments.admin.settings.field.retention"), Kind: "number", Required: true, Value: num(s.RetentionDays)},
			Field{Name: "shop_zone", Label: K("appointments.admin.settings.field.zone"), Kind: "text", Required: true, Value: s.ShopZone},
			Field{Name: "currency", Label: K("appointments.admin.settings.field.currency"), Kind: "text", Required: true, Value: s.Currency},
		),
	), nil
}
