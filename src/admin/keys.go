package admin

// View ids (the manifest entries' "view" values).
const (
	ViewResources = "appointments.resources"
	ViewServices  = "appointments.services"
	ViewHours     = "appointments.hours"
	ViewTimeOff   = "appointments.timeoff"
	ViewSettings  = "appointments.settings"
)

// Actions.
const (
	ActSaveResource  = "save_resource"
	ActSaveService   = "save_service"
	ActShowResource  = "show_resource"
	ActShowHours     = "show_hours"
	ActAddHours      = "add_hours"
	ActClearHours    = "clear_hours"
	ActAddTimeOff    = "add_time_off"
	ActRemoveTimeOff = "remove_time_off"
	ActSaveSettings  = "save_settings"
)

// KeyOverrideReasonHint is the hint under the conflict-override reason field
// (appointment-v1 section 1, binding): "Do not write client or health
// details". The calendar slice uses it; it lives here with the other keys.
const KeyOverrideReasonHint = "appointments.override.reason_hint"

const (
	kTitleResources = "appointments.admin.resources.title"
	kTitleServices  = "appointments.admin.services.title"
	kTitleHours     = "appointments.admin.hours.title"
	kTitleTimeOff   = "appointments.admin.timeoff.title"
	kTitleSettings  = "appointments.admin.settings.title"
)
