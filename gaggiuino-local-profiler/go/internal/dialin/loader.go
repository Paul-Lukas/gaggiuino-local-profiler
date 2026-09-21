package dialin

import (
	"strings"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// BuildComparableShots converts raw shots.Shot values (as returned by
// shots.Repository.FindAllExcludingTrash, full annotation+datapoints
// included) into this package's plain ComparableShot shape, resolving each
// shot's recorded grind setting into "what it would read on the grinder
// today" via library.RelativeGrindSetting — every consumer of
// ComparableShot (regression, offsets, best-combo) needs that correction
// applied uniformly, so it happens once here rather than at every call
// site. Shots this package can't use at all (no grinder name, no parseable
// grind setting) are silently dropped, not zero-valued — a caller filtering
// on Grind > 0 would already exclude them, but dropping them outright keeps
// len(result) meaningful as "usable shots", matching how the backtest
// report's sample-size counts are meant to read.
func BuildComparableShots(allShots []shots.Shot, lib library.Library) []ComparableShot {
	grindersByName := make(map[string]library.Entity, len(lib.Grinders))
	for _, g := range lib.Grinders {
		if name, ok := g["name"].(string); ok && name != "" {
			grindersByName[strings.ToLower(name)] = g
		}
	}

	out := make([]ComparableShot, 0, len(allShots))
	for _, shot := range allShots {
		ann, _ := shot["annotation"].(map[string]any)
		if ann == nil {
			continue
		}
		grinderName, _ := ann["grinder"].(string)
		if grinderName == "" {
			continue
		}
		grindRaw, ok := shots.ParseGrindNum(strOf(ann["grindSetting"]))
		if !ok {
			continue
		}
		timestamp, _ := shot["timestamp"].(int64)

		grind := grindRaw
		if grinder, found := grindersByName[strings.ToLower(grinderName)]; found {
			if normalized, ok := library.RelativeGrindSetting(grinder, grindRaw, timestamp*1000); ok {
				grind = normalized
			}
		}

		durationRaw, _ := shot["duration"].(int64)
		cs := ComparableShot{
			ShotID:       idOf(shot, "id"),
			GrinderName:  grinderName,
			Grind:        grind,
			DurationS:    float64(durationRaw) / 10,
			DoseG:        floatOf(ann["dose"]),
			BasketID:     intOf(ann["basketId"]),
			PuckScreenID: intOf(ann["puckScreenId"]),
			ProfileName:  strOf(shot["profileName"]),
			RecipeID:     intOf(ann["recipeId"]),
			Timestamp:    timestamp,
			BeanID:       intOf(ann["beanId"]),
		}
		if cs.DoseG > 0 {
			if weight, ok := finalWeightG(shot); ok && weight > 0 {
				cs.RatioActual = weight / cs.DoseG
			}
		}
		out = append(out, cs)
	}
	return out
}

// finalWeightG reads a shot's final brew weight from its datapoints —
// shotWeight wins over weight when both are present, mirroring
// score.go's scoreSeries.weight precedence (see that file's own doc
// comment). Values are ×10-scaled like every other datapoints series here.
func finalWeightG(shot shots.Shot) (float64, bool) {
	dp := shots.DatapointsMap(shot)
	series, ok := dp["shotWeight"]
	if !ok || series == nil {
		series, ok = dp["weight"]
	}
	if !ok || series == nil {
		return 0, false
	}
	arr, ok := series.([]any)
	if !ok || len(arr) == 0 {
		return 0, false
	}
	last := floatOf(arr[len(arr)-1])
	if last == 0 {
		return 0, false
	}
	return last / 10, true
}

func idOf(m map[string]any, key string) int64 {
	return intOf(m[key])
}

func intOf(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	default:
		return 0
	}
}

func floatOf(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	default:
		return 0
	}
}

func strOf(v any) string {
	s, _ := v.(string)
	return s
}
