// Package dialin is a statistical grind-setting recommendation engine — the
// planned successor to grind.js's suggestGrindForBeanGrinder (a simple
// "best historical (grinder,grind) combo, else knownGrindSettings, else
// last shot" heuristic). This package stays purely local (no external
// service/ML dependency, works entirely off a user's own shot history) and
// is deliberately NOT wired into any HTTP route yet — see
// cmd/dialin-backtest for the tool that validates this against a real
// user's shot history before any API/UI work happens.
//
// Core idea: rather than repeating whichever grind setting scored well in
// the past, regress grind→duration for the comparable shot set and solve
// for the grind value the trend predicts would hit the recipe's actual
// target time — "which grind gets me to the recipe's target", not "which
// grind did well before". Basket/puckscreen/profile are folded in as
// learned additive offsets (see basketOffset/puckScreenOffset/
// profileOffset) rather than as regression features in their own right:
// with the sample sizes a home install actually has (tens, not thousands,
// of shots), a multi-variable regression would be badly overfit long
// before a fixed-effects-style offset — computed once from whichever shots
// happen to share a bean+grinder but differ on that one accessory — runs
// out of data.
package dialin

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// ComparableShot is one shot's data as this package needs it, already
// resolved out of the raw shots.Shot/annotation map shape (see
// BuildComparableShots) and zero-point normalized (see NormalizeGrind) —
// this package works only with these plain, already-clean values so its
// statistics stay decoupled from the shots/library packages' map-based
// storage representation.
type ComparableShot struct {
	ShotID       int64
	BeanID       int64 // 0 = unknown/unset
	GrinderName  string
	Grind        float64 // zero-point-normalized "as it reads today"
	DurationS    float64
	RatioActual  float64 // yield/dose; 0 if not computable
	DoseG        float64
	BasketID     int64 // 0 = unset
	PuckScreenID int64 // 0 = unset
	ProfileName  string
	RecipeID     int64 // 0 = unset — not read by Recommend itself, carried for callers (e.g. the backtest tool) that need to look up the shot's own recipe target
	Timestamp    int64 // unix seconds
}

// RecipeTarget is the subset of a Recipe entity Recommend needs — pulled
// out as its own small struct (rather than taking library.Entity directly)
// so this package never has to know the Recipe map's key names.
type RecipeTarget struct {
	TargetTimeS  *float64
	TargetYieldG *float64
	TargetDoseG  *float64
}

// impliedTargetTimeS derives a target duration when the recipe has no
// explicit targetTime_s: TargetYieldG/TargetDoseG gives a target ratio,
// which combined with the comparable-shot set's own average
// yield-per-second (a rough "how fast does coffee flow for this
// bean/grinder" prior) implies a duration. Falls back to the classic
// 25-35s espresso band's midpoint (30s) when neither the recipe nor the
// shot set has enough to say anything — this is the "ein paar
// Grundannahmen" the design leans on when data is thin.
func impliedTargetTimeS(target RecipeTarget, comparable []ComparableShot) float64 {
	if target.TargetTimeS != nil {
		return *target.TargetTimeS
	}
	if target.TargetYieldG != nil && target.TargetDoseG != nil && *target.TargetDoseG > 0 {
		targetRatio := *target.TargetYieldG / *target.TargetDoseG
		if avgRatio, avgDuration, ok := avgRatioAndDuration(comparable); ok && avgRatio > 0 {
			// Scale the comparable set's average duration by how much
			// longer/shorter the target ratio implies relative to what
			// those shots actually averaged — a rough but data-grounded
			// scaling instead of guessing a flat number.
			return avgDuration * (targetRatio / avgRatio)
		}
	}
	return 30
}

func avgRatioAndDuration(shots []ComparableShot) (ratio, duration float64, ok bool) {
	var sumRatio, sumDuration float64
	var n int
	for _, s := range shots {
		if s.RatioActual <= 0 || s.DurationS <= 0 {
			continue
		}
		sumRatio += s.RatioActual
		sumDuration += s.DurationS
		n++
	}
	if n == 0 {
		return 0, 0, false
	}
	return sumRatio / float64(n), sumDuration / float64(n), true
}

// Recommendation is Recommend's result. Method/Confidence/SampleSize exist
// so a caller (or the backtest tool) can tell how much to trust the
// number, not just what it is.
type Recommendation struct {
	GrindSetting float64
	Method       string // "regression" | "best-combo" | "known-setting" | "last-shot" | "none"
	Confidence   string // "high" | "medium" | "low" | ""
	SampleSize   int

	// Offsets actually applied on top of the base (regression or
	// best-combo) value — reported separately so a caller/backtest can see
	// how much of the final number came from the accessory corrections
	// versus the base estimate.
	BasketOffset     float64
	PuckScreenOffset float64
	ProfileOffset    float64
}

// minRegressionSamples is the minimum comparable-shot count before trusting
// a linear regression over the simpler best-combo fallback — below this, a
// two-point "line" is just noise dressed up as a trend.
const minRegressionSamples = 5

// minOffsetSamples is the minimum number of same-bean+grinder shots an
// accessory group (a specific basket/puckscreen/profile value) needs before
// its offset is trusted at all — below this a single outlier shot could
// swing the whole correction.
const minOffsetSamples = 2

// Recommend picks a grind setting for the given bean+grinder+accessories,
// aimed at target, using history — see the package doc comment for the
// overall approach and the priority chain below.
func Recommend(beanID int64, grinderName string, basketID, puckScreenID int64, profileName string, target RecipeTarget, allShots []ComparableShot) Recommendation {
	sameBeanGrinder := filterShots(allShots, func(s ComparableShot) bool {
		return s.BeanID == beanID && strings.EqualFold(s.GrinderName, grinderName) && s.Grind > 0 && s.DurationS > 0
	})

	basketOffset := accessoryOffset(allShots, func(s ComparableShot) (key int64, ok bool) { return s.BasketID, s.BasketID != 0 })[basketID]
	puckScreenOffset := accessoryOffset(allShots, func(s ComparableShot) (key int64, ok bool) { return s.PuckScreenID, s.PuckScreenID != 0 })[puckScreenID]
	profileOffset := accessoryOffsetStr(allShots, func(s ComparableShot) (key string, ok bool) { return s.ProfileName, s.ProfileName != "" })[profileName]

	if len(sameBeanGrinder) >= minRegressionSamples {
		targetTimeS := impliedTargetTimeS(target, sameBeanGrinder)
		if slope, intercept, ok := linearRegression(sameBeanGrinder); ok && slope != 0 {
			grind := (targetTimeS - intercept) / slope
			return Recommendation{
				GrindSetting:     grind + basketOffset + puckScreenOffset + profileOffset,
				Method:           "regression",
				Confidence:       confidenceFor(len(sameBeanGrinder)),
				SampleSize:       len(sameBeanGrinder),
				BasketOffset:     basketOffset,
				PuckScreenOffset: puckScreenOffset,
				ProfileOffset:    profileOffset,
			}
		}
	}

	if combo, ok := bestCombo(sameBeanGrinder); ok {
		return Recommendation{
			GrindSetting:     combo.grind + basketOffset + puckScreenOffset + profileOffset,
			Method:           "best-combo",
			Confidence:       confidenceFor(combo.sampleSize),
			SampleSize:       combo.sampleSize,
			BasketOffset:     basketOffset,
			PuckScreenOffset: puckScreenOffset,
			ProfileOffset:    profileOffset,
		}
	}

	// Widen to same-grinder, any bean — knownGrindSettings-equivalent when
	// this exact bean has too little history: the grinder's own recent
	// calibration matters more here than bean identity.
	sameGrinder := filterShots(allShots, func(s ComparableShot) bool {
		return strings.EqualFold(s.GrinderName, grinderName) && s.Grind > 0
	})
	sort.Slice(sameGrinder, func(i, j int) bool { return sameGrinder[i].Timestamp > sameGrinder[j].Timestamp })
	if len(sameGrinder) > 0 {
		return Recommendation{
			GrindSetting:     sameGrinder[0].Grind + basketOffset + puckScreenOffset + profileOffset,
			Method:           "last-shot",
			Confidence:       "low",
			SampleSize:       1,
			BasketOffset:     basketOffset,
			PuckScreenOffset: puckScreenOffset,
			ProfileOffset:    profileOffset,
		}
	}

	return Recommendation{Method: "none"}
}

func confidenceFor(sampleSize int) string {
	switch {
	case sampleSize >= 10:
		return "high"
	case sampleSize >= minRegressionSamples:
		return "medium"
	default:
		return "low"
	}
}

func filterShots(shots []ComparableShot, pred func(ComparableShot) bool) []ComparableShot {
	out := make([]ComparableShot, 0, len(shots))
	for _, s := range shots {
		if pred(s) {
			out = append(out, s)
		}
	}
	return out
}

// linearRegression fits DurationS ~ Grind (ordinary least squares) over the
// given shots, returning slope+intercept such that
// DurationS ≈ slope*Grind + intercept. ok=false when fewer than 2 distinct
// grind values exist (a vertical/degenerate fit).
func linearRegression(shots []ComparableShot) (slope, intercept float64, ok bool) {
	n := float64(len(shots))
	if n < 2 {
		return 0, 0, false
	}
	var sumX, sumY, sumXY, sumXX float64
	for _, s := range shots {
		sumX += s.Grind
		sumY += s.DurationS
		sumXY += s.Grind * s.DurationS
		sumXX += s.Grind * s.Grind
	}
	denom := n*sumXX - sumX*sumX
	if math.Abs(denom) < 1e-9 {
		return 0, 0, false // all shots at (near) the same grind value
	}
	slope = (n*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / n
	return slope, intercept, true
}

type gradedCombo struct {
	grind      float64
	sampleSize int
}

// bestCombo mirrors grind.js's calcBestGrindCombosForBean: bucket shots by
// grind setting (rounded to the nearest 0.5), pick the bucket with the best
// average duration-proximity-to-band score (using the same 25-35s ideal
// band CalcShotScoreDetail's duration dimension uses, so this stays
// consistent with the app's own scoring — not a second, diverging notion
// of "good"), requiring at least minOffsetSamples shots in that bucket.
func bestCombo(shots []ComparableShot) (gradedCombo, bool) {
	type bucket struct {
		sumScore float64
		count    int
	}
	buckets := map[float64]*bucket{}
	for _, s := range shots {
		key := math.Round(s.Grind*2) / 2
		b := buckets[key]
		if b == nil {
			b = &bucket{}
			buckets[key] = b
		}
		b.sumScore += durationBandScore(s.DurationS)
		b.count++
	}
	var best gradedCombo
	bestAvg := -1.0
	for grind, b := range buckets {
		if b.count < minOffsetSamples {
			continue
		}
		avg := b.sumScore / float64(b.count)
		if avg > bestAvg {
			bestAvg = avg
			best = gradedCombo{grind: grind, sampleSize: b.count}
		}
	}
	if bestAvg < 0 {
		return gradedCombo{}, false
	}
	return best, true
}

// durationBandScore mirrors CalcShotScoreDetail's duration dimension
// (25-35s = 100, linear falloff outside it) — reused here so "best
// historical combo" means the same thing this package's regression path
// and the app's own shot score already agree on.
func durationBandScore(secs float64) float64 {
	switch {
	case secs >= 25 && secs <= 35:
		return 100
	case secs < 25:
		return math.Max(15, 100-(25-secs)*8)
	default:
		return math.Max(15, 100-(secs-35)*8)
	}
}

// accessoryOffset computes a fixed-effects-style correction per accessory
// value (basket/puckscreen id): among shots sharing the same bean+grinder,
// group by the accessory's value, take each group's median grind, and the
// offset is that median minus the overall (bean+grinder-scoped) median —
// averaged (weighted by sample size) across every bean+grinder combination
// where the accessory value actually varies. This is what lets a
// basket/puckscreen offset apply even to a bean+grinder pairing that's
// never been tried with that specific accessory: the offset is learned
// globally, not per-bean.
func accessoryOffset(shots []ComparableShot, keyOf func(ComparableShot) (int64, bool)) map[int64]float64 {
	type group struct {
		beanID, grinderKey string
	}
	byGroup := map[group]map[int64][]float64{}
	for _, s := range shots {
		if s.Grind <= 0 {
			continue
		}
		k, ok := keyOf(s)
		if !ok {
			continue
		}
		g := group{strconv.FormatInt(s.BeanID, 10), strings.ToLower(s.GrinderName)}
		if byGroup[g] == nil {
			byGroup[g] = map[int64][]float64{}
		}
		byGroup[g][k] = append(byGroup[g][k], s.Grind)
	}

	type weighted struct {
		sum, weight float64
	}
	perKey := map[int64]*weighted{}
	for _, byAccessory := range byGroup {
		if len(byAccessory) < 2 {
			continue // accessory doesn't vary within this bean+grinder — no signal
		}
		var allGrinds []float64
		for _, vs := range byAccessory {
			allGrinds = append(allGrinds, vs...)
		}
		overall := median(allGrinds)
		for k, vs := range byAccessory {
			if len(vs) < minOffsetSamples {
				continue
			}
			offset := median(vs) - overall
			w := perKey[k]
			if w == nil {
				w = &weighted{}
				perKey[k] = w
			}
			weight := float64(len(vs))
			w.sum += offset * weight
			w.weight += weight
		}
	}
	out := make(map[int64]float64, len(perKey))
	for k, w := range perKey {
		if w.weight > 0 {
			out[k] = w.sum / w.weight
		}
	}
	return out
}

// accessoryOffsetStr is accessoryOffset's string-keyed twin, for
// profileName (which has no library id).
func accessoryOffsetStr(shots []ComparableShot, keyOf func(ComparableShot) (string, bool)) map[string]float64 {
	type group struct {
		beanID, grinderKey string
	}
	byGroup := map[group]map[string][]float64{}
	for _, s := range shots {
		if s.Grind <= 0 {
			continue
		}
		k, ok := keyOf(s)
		if !ok {
			continue
		}
		g := group{strconv.FormatInt(s.BeanID, 10), strings.ToLower(s.GrinderName)}
		if byGroup[g] == nil {
			byGroup[g] = map[string][]float64{}
		}
		byGroup[g][k] = append(byGroup[g][k], s.Grind)
	}

	type weighted struct {
		sum, weight float64
	}
	perKey := map[string]*weighted{}
	for _, byAccessory := range byGroup {
		if len(byAccessory) < 2 {
			continue
		}
		var allGrinds []float64
		for _, vs := range byAccessory {
			allGrinds = append(allGrinds, vs...)
		}
		overall := median(allGrinds)
		for k, vs := range byAccessory {
			if len(vs) < minOffsetSamples {
				continue
			}
			offset := median(vs) - overall
			w := perKey[k]
			if w == nil {
				w = &weighted{}
				perKey[k] = w
			}
			weight := float64(len(vs))
			w.sum += offset * weight
			w.weight += weight
		}
	}
	out := make(map[string]float64, len(perKey))
	for k, w := range perKey {
		if w.weight > 0 {
			out[k] = w.sum / w.weight
		}
	}
	return out
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}
