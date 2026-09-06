package library

import (
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// TestComputeBeanRemaining_DistinctBagsWithoutOpenedAt_NotMisattributed
// (#901 code review): sameBag must distinguish two bags that both lack
// openedAt (the normal state for any bag recorded before #456 introduced
// bag-open tracking) by identity, not by comparing their openedAt VALUES —
// both are the zero value, so a value comparison would wrongly treat every
// openedAt-less bag as "the same bag" as every other one.
//
// Setup: a bean with two such bags — a stale one, then the current active
// one (bags[len-1]) — and a single dose recorded against the bean. Neither
// bag has openedAt, so bagAtTime resolves the dose to bags[0] (the first
// bag on record, matching the JS original's stable-sort tie-break — see
// LibraryService.js's computeBeanRemaining). Since bags[0] is NOT the
// active bag, the dose must NOT be counted against the active bag's stock:
// remaining must stay at the bean's full stock_g, not stock_g-dose.
func TestComputeBeanRemaining_DistinctBagsWithoutOpenedAt_NotMisattributed(t *testing.T) {
	bean := Entity{
		"id": int64(1), "name": "Test Bean", "stock_g": float64(200),
		"bags": []any{
			Entity{"id": int64(101)}, // stale bag on record, no openedAt
			Entity{"id": int64(102)}, // current active bag, also no openedAt
		},
	}
	dose := 20.0
	beanID := int64(1)
	doseRows := []shots.AnnotatedDose{
		{BeanID: &beanID, Dose: &dose, Timestamp: 1000},
	}

	remaining, ok := ComputeBeanRemaining(bean, doseRows, []Entity{bean})
	if !ok {
		t.Fatalf("ComputeBeanRemaining: ok = false, want true (bean has positive stock_g)")
	}
	if remaining != 200 {
		t.Fatalf("remaining = %d, want 200 — the dose resolves to a DIFFERENT (openedAt-less) bag than the active one and must not be deducted from it", remaining)
	}
}

// TestComputeBeanRemaining_MultiBagWithStock_SumsAllBags verifies that the
// new per-bag summation correctly adds remaining stock from older bags that
// still carry positive stock_g (the pre-fix bug returned only the active
// bag's remaining, silently ignoring partially-consumed older bags).
func TestComputeBeanRemaining_MultiBagWithStock_SumsAllBags(t *testing.T) {
	// bag1: 250g, no consumption → 250g remaining
	// bag2: 250g (active), 70g consumed → 180g remaining
	// expected total: 430g
	dose := 70.0
	beanID := int64(1)
	bag1OpenedAt := int64(0)
	bag2OpenedAt := int64(10000 * 1000)
	bean := Entity{
		"id": beanID, "name": "Brasil", "stock_g": float64(250),
		"bags": []any{
			Entity{"id": int64(1), "stock_g": float64(250), "openedAt": bag1OpenedAt},
			Entity{"id": int64(2), "stock_g": float64(250), "openedAt": bag2OpenedAt},
		},
	}
	// timestamp 11000 → shotMs = 11_000_000 > bag2OpenedAt → bag2
	doseRows := []shots.AnnotatedDose{
		{BeanID: &beanID, Dose: &dose, Timestamp: 11000},
	}
	remaining, ok := ComputeBeanRemaining(bean, doseRows, []Entity{bean})
	if !ok {
		t.Fatalf("ComputeBeanRemaining: ok = false")
	}
	if remaining != 430 {
		t.Fatalf("remaining = %d, want 430 (250 from bag1 + 180 from bag2)", remaining)
	}
}

// TestComputeBeanRemaining_MultiBagWithStock_FIFOOverflow verifies that
// overflow from one bag period carries to the next with no per-bag clamping
// (FIFO model: totalStock − totalConsumed, clamped at 0 only at the end).
func TestComputeBeanRemaining_MultiBagWithStock_FIFOOverflow(t *testing.T) {
	// bag1: 100g, 120g consumed → 20g overflow into bag2 (FIFO)
	// bag2: 250g (active), 20g consumed
	// FIFO: totalStock=350, totalConsumed=140 → max(0, 350-140) = 210
	beanID := int64(1)
	bag1OpenedAt := int64(0)
	bag2OpenedAt := int64(10000 * 1000)
	bean := Entity{
		"id": beanID, "name": "Brasil", "stock_g": float64(250),
		"bags": []any{
			Entity{"id": int64(1), "stock_g": float64(100), "openedAt": bag1OpenedAt},
			Entity{"id": int64(2), "stock_g": float64(250), "openedAt": bag2OpenedAt},
		},
	}
	dose1, dose2 := 120.0, 20.0
	doseRows := []shots.AnnotatedDose{
		{BeanID: &beanID, Dose: &dose1, Timestamp: 5000},  // bag1
		{BeanID: &beanID, Dose: &dose2, Timestamp: 11000}, // bag2
	}
	remaining, ok := ComputeBeanRemaining(bean, doseRows, []Entity{bean})
	if !ok {
		t.Fatalf("ComputeBeanRemaining: ok = false")
	}
	if remaining != 210 {
		t.Fatalf("remaining = %d, want 210 (FIFO: 350g total - 140g consumed)", remaining)
	}
}

// TestSameBag_DistinctMapsWithEqualFieldsAreNotSame is a narrower,
// function-level companion to the ComputeBeanRemaining test above: two
// distinct Entity maps with identical (empty) openedAt must not compare
// equal by sameBag, matching JS's `===` object-reference semantics.
func TestSameBag_DistinctMapsWithEqualFieldsAreNotSame(t *testing.T) {
	a := Entity{"id": int64(1)}
	b := Entity{"id": int64(2)}
	if sameBag(a, b) {
		t.Fatal("sameBag(a, b) = true for two distinct bags that both lack openedAt")
	}
	if !sameBag(a, a) {
		t.Fatal("sameBag(a, a) = false; the exact same bag must compare equal to itself")
	}
}
