package engine

import "errors"

// Sentinels. The strings are stable wire codes; the UI slice maps them to
// locale keys.
var (
	ErrConflict             = errors.New("conflict")
	ErrInvalid              = errors.New("invalid")
	ErrOverrideNotAllowed   = errors.New("override_not_allowed")
	ErrReasonRequired       = errors.New("reason_required")
	ErrTransitionNotAllowed = errors.New("transition_not_allowed")
	ErrNoShowTooEarly       = errors.New("no_show_too_early")
	ErrSaleRefRequired      = errors.New("sale_ref_required")
	ErrNoServices           = errors.New("no_services")
	ErrInactiveService      = errors.New("inactive_service")
	ErrResourceNotEligible  = errors.New("resource_not_eligible")
	ErrInvalidPolicy        = errors.New("invalid_policy")
	ErrDepositTooLarge      = errors.New("deposit_too_large")
	ErrDepositInvalid       = errors.New("deposit_invalid")
	ErrAmountTooLarge       = errors.New("amount_too_large")
)
