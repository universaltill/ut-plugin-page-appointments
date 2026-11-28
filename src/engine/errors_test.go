package engine

import "testing"

func TestErrors_WireCodes(t *testing.T) {
	want := map[error]string{
		ErrConflict: "conflict", ErrInvalid: "invalid", ErrOverrideNotAllowed: "override_not_allowed",
		ErrReasonRequired: "reason_required", ErrTransitionNotAllowed: "transition_not_allowed",
		ErrNoShowTooEarly: "no_show_too_early", ErrSaleRefRequired: "sale_ref_required",
		ErrNoServices: "no_services", ErrInactiveService: "inactive_service",
		ErrResourceNotEligible: "resource_not_eligible", ErrInvalidPolicy: "invalid_policy",
		ErrDepositTooLarge: "deposit_too_large", ErrDepositInvalid: "deposit_invalid",
		ErrAmountTooLarge: "amount_too_large",
	}
	if len(want) != 14 {
		t.Fatal("expected 14 sentinels")
	}
	for e, s := range want {
		if e.Error() != s {
			t.Errorf("%v != %q", e, s)
		}
	}
}
