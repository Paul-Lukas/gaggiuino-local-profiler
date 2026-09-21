package dialin

import (
	"math"
	"testing"
)

// syntheticShots builds a comparable-shot set with a known, exact linear
// grind→duration relationship (duration = slope*grind + intercept) plus an
// optional per-basket offset baked in — lets the regression/offset tests
// assert against an exact expected answer instead of a fuzzy range.
func syntheticShots(n int, startGrind, slope, intercept float64, basketOffsets map[int64]float64) []ComparableShot {
	out := make([]ComparableShot, 0, n)
	id := int64(1)
	for i := 0; i < n; i++ {
		grind := startGrind + float64(i)*0.5
		for basketID, offset := range basketOffsets {
			effectiveGrind := grind + offset
			out = append(out, ComparableShot{
				ShotID:      id,
				BeanID:      1,
				GrinderName: "Niche Zero",
				Grind:       effectiveGrind,
				DurationS:   slope*effectiveGrind + intercept,
				DoseG:       18,
				BasketID:    basketID,
				Timestamp:   int64(1000 + i),
			})
			id++
		}
	}
	return out
}

func TestRecommend_RegressionHitsExactTargetOnCleanLinearData(t *testing.T) {
	// duration = 2*grind - 10 (e.g. grind 20 -> 30s). Solve for target 30s.
	shots := syntheticShots(8, 15, 2, -10, map[int64]float64{0: 0})
	target := 30.0
	rec := Recommend(1, "Niche Zero", 0, 0, "", RecipeTarget{TargetTimeS: &target}, shots)

	if rec.Method != "regression" {
		t.Fatalf("Method = %q, want regression (sample size %d)", rec.Method, len(shots))
	}
	wantGrind := 20.0 // (30 - (-10)) / 2
	if math.Abs(rec.GrindSetting-wantGrind) > 0.01 {
		t.Fatalf("GrindSetting = %v, want %v", rec.GrindSetting, wantGrind)
	}
}

func TestRecommend_TooFewSamplesFallsBackToBestCombo(t *testing.T) {
	// Only 3 shots for this bean+grinder — below minRegressionSamples (5).
	shots := []ComparableShot{
		{ShotID: 1, BeanID: 1, GrinderName: "Niche Zero", Grind: 18, DurationS: 28, DoseG: 18, Timestamp: 1000},
		{ShotID: 2, BeanID: 1, GrinderName: "Niche Zero", Grind: 18, DurationS: 29, DoseG: 18, Timestamp: 1001},
		{ShotID: 3, BeanID: 1, GrinderName: "Niche Zero", Grind: 22, DurationS: 45, DoseG: 18, Timestamp: 1002},
	}
	target := 30.0
	rec := Recommend(1, "Niche Zero", 0, 0, "", RecipeTarget{TargetTimeS: &target}, shots)

	if rec.Method != "best-combo" {
		t.Fatalf("Method = %q, want best-combo", rec.Method)
	}
	// Grind 18's two shots (28s, 29s) are both inside the 25-35s ideal band
	// and score 100; grind 22's single shot (45s) is way outside it and
	// scores much lower — 18 should win even though it's not literally 30s.
	if rec.GrindSetting != 18 {
		t.Fatalf("GrindSetting = %v, want 18 (best-scoring bucket)", rec.GrindSetting)
	}
}

func TestRecommend_NoBeanGrinderDataFallsBackToLastShotOnSameGrinder(t *testing.T) {
	shots := []ComparableShot{
		// Different bean (2), same grinder — no data at all for bean 1.
		{ShotID: 1, BeanID: 2, GrinderName: "Niche Zero", Grind: 21, DurationS: 30, DoseG: 18, Timestamp: 1000},
		{ShotID: 2, BeanID: 2, GrinderName: "Niche Zero", Grind: 23, DurationS: 32, DoseG: 18, Timestamp: 2000}, // most recent
	}
	rec := Recommend(1, "Niche Zero", 0, 0, "", RecipeTarget{}, shots)

	if rec.Method != "last-shot" {
		t.Fatalf("Method = %q, want last-shot", rec.Method)
	}
	if rec.GrindSetting != 23 {
		t.Fatalf("GrindSetting = %v, want 23 (the most recent shot's grind)", rec.GrindSetting)
	}
	if rec.Confidence != "low" {
		t.Fatalf("Confidence = %q, want low", rec.Confidence)
	}
}

func TestRecommend_NoDataAtAllReturnsNone(t *testing.T) {
	rec := Recommend(1, "Niche Zero", 0, 0, "", RecipeTarget{}, nil)
	if rec.Method != "none" {
		t.Fatalf("Method = %q, want none", rec.Method)
	}
}

func TestRecommend_BasketOffsetLearnedFromCrossReferencedShots(t *testing.T) {
	// Bean 1 + grinder "Niche Zero": basket 10 needs +1.0 grind finer... err
	// coarser (offset convention: basket's own median minus overall median)
	// relative to basket 20, learned from bean 1's own history. Then a
	// SECOND bean+grinder pairing (bean 2) that's only ever used basket 10
	// should still get that offset applied, since it's learned globally.
	shots := []ComparableShot{
		// Bean 1, basket 10: median grind 21.
		{ShotID: 1, BeanID: 1, GrinderName: "Niche Zero", Grind: 20, DurationS: 28, DoseG: 18, BasketID: 10, Timestamp: 1000},
		{ShotID: 2, BeanID: 1, GrinderName: "Niche Zero", Grind: 21, DurationS: 29, DoseG: 18, BasketID: 10, Timestamp: 1001},
		{ShotID: 3, BeanID: 1, GrinderName: "Niche Zero", Grind: 22, DurationS: 30, DoseG: 18, BasketID: 10, Timestamp: 1002},
		// Bean 1, basket 20: median grind 19 (2.0 finer than basket 10's 21).
		{ShotID: 4, BeanID: 1, GrinderName: "Niche Zero", Grind: 18, DurationS: 28, DoseG: 18, BasketID: 20, Timestamp: 1003},
		{ShotID: 5, BeanID: 1, GrinderName: "Niche Zero", Grind: 19, DurationS: 29, DoseG: 18, BasketID: 20, Timestamp: 1004},
		{ShotID: 6, BeanID: 1, GrinderName: "Niche Zero", Grind: 20, DurationS: 30, DoseG: 18, BasketID: 20, Timestamp: 1005},
	}
	basketOffsets := accessoryOffset(shots, func(s ComparableShot) (int64, bool) { return s.BasketID, s.BasketID != 0 })

	// Overall median across both baskets' 6 grinds (18,19,20,20,21,22) = 20.
	// basket 10's median (21) - overall (20) = +1.
	// basket 20's median (19) - overall (20) = -1.
	if got := basketOffsets[10]; math.Abs(got-1) > 0.01 {
		t.Fatalf("basket 10 offset = %v, want +1", got)
	}
	if got := basketOffsets[20]; math.Abs(got+1) > 0.01 {
		t.Fatalf("basket 20 offset = %v, want -1", got)
	}
}

func TestRecommend_OffsetNotAppliedWhenAccessoryDoesNotVaryWithinAnyBeanGrinder(t *testing.T) {
	// Every shot uses the same basket — nothing to learn an offset from.
	shots := []ComparableShot{
		{ShotID: 1, BeanID: 1, GrinderName: "Niche Zero", Grind: 20, DurationS: 28, DoseG: 18, BasketID: 10, Timestamp: 1000},
		{ShotID: 2, BeanID: 1, GrinderName: "Niche Zero", Grind: 21, DurationS: 29, DoseG: 18, BasketID: 10, Timestamp: 1001},
	}
	offsets := accessoryOffset(shots, func(s ComparableShot) (int64, bool) { return s.BasketID, s.BasketID != 0 })
	if _, exists := offsets[10]; exists {
		t.Fatalf("offsets[10] should not exist when basket never varies: %+v", offsets)
	}
}

func TestLinearRegression_DegenerateAllSameGrindReturnsNotOK(t *testing.T) {
	shots := []ComparableShot{
		{Grind: 20, DurationS: 28},
		{Grind: 20, DurationS: 30},
		{Grind: 20, DurationS: 29},
	}
	_, _, ok := linearRegression(shots)
	if ok {
		t.Fatal("linearRegression should report ok=false when every shot has the same grind (no slope is determinable)")
	}
}

func TestConfidenceFor(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "low"}, {4, "low"}, {5, "medium"}, {9, "medium"}, {10, "high"}, {50, "high"},
	}
	for _, c := range cases {
		if got := confidenceFor(c.n); got != c.want {
			t.Errorf("confidenceFor(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestMedian(t *testing.T) {
	if got := median([]float64{1, 2, 3}); got != 2 {
		t.Errorf("median odd = %v, want 2", got)
	}
	if got := median([]float64{1, 2, 3, 4}); got != 2.5 {
		t.Errorf("median even = %v, want 2.5", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("median empty = %v, want 0", got)
	}
}
