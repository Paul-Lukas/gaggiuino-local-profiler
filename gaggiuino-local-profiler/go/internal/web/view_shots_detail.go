package web

import (
	"fmt"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/web/templates"
)

// This file builds templates.ShotDetail — the shot-detail (right-hand)
// column of Phase B's (#901) Shots master-detail view — the same
// projection role view.go's toShotRow plays for the (left-hand) compact
// list, just against shots.ComputeShotMetrics/ComputeGrindAdvice instead of
// score alone. See templates/shots_detail.templ's own doc comment for the
// visual reference (public-src/views/shots/index.js's updateView()) and
// what did/didn't carry over.

// formatSecondsMMSS renders a float64 seconds value (detectPhases'
// preinfusion/extraction split, in whole seconds with a fractional part
// from the underlying tenths-of-a-second sampling) as "MM:SS" — the same
// floor-based rule formatDuration uses for the tenths-int64 shot duration,
// just operating on a float64 instead.
func formatSecondsMMSS(secs float64) string {
	total := int64(secs)
	if total < 0 {
		total = 0
	}
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

// grinderGrindLabel ports the non-baseline half of public-src/views/shots/
// utils.js's buildGrinderGrindLabel: grinder name + grind setting combined
// into one label. The baseline half ("zuletzt X") needs the full shot
// history for the same bean (findPreviousShotForBean) — out of scope for a
// single shot's own detail view, same boundary CalcShotScoreDetail's bean-
// target resolution already draws.
func grinderGrindLabel(grinder, grindSetting string) string {
	switch {
	case grinder != "" && grindSetting != "":
		return grinder + " · " + grindSetting
	case grindSetting != "":
		return "Grind " + grindSetting
	default:
		return grinder
	}
}

// hasChartData reports whether shot carries any recorded time/pressure
// samples at all — gates templates.ShotDetail.HasChart, so the detail
// panel shows an empty-state message instead of an inert empty canvas for
// a shot with no datapoints (e.g. one imported without curve data).
func hasChartData(shot shots.Shot) bool {
	dp, ok := shot["datapoints"].(map[string]any)
	if !ok {
		return false
	}
	arr, ok := dp["timeInShot"].([]any)
	return ok && len(arr) > 0
}

// toShotDetail builds a templates.ShotDetail from a hydrated shot plus its
// pre-computed score — the detail-panel equivalent of toShotRow.
func toShotDetail(shot shots.Shot, score *int) templates.ShotDetail {
	detail := templates.ShotDetail{}

	if id, ok := shot["id"].(int64); ok {
		detail.ID = id
	}
	if ts, ok := shot["timestamp"].(int64); ok {
		detail.DateTime = time.Unix(ts, 0).Format("02.01.2006 15:04")
	}
	if pn, ok := shot["profileName"].(string); ok && pn != "" {
		detail.ProfileName = pn
	} else {
		detail.ProfileName = "Unknown Profile"
	}

	detail.Score = score
	if score != nil {
		detail.ScoreClass = scoreClass(*score)
	}

	metrics := shots.ComputeShotMetrics(shot)
	if advice := shots.ComputeGrindAdvice(shot, metrics); advice != nil {
		detail.VerdictIcon = advice.Icon
		detail.VerdictText = advice.Text
	} else {
		detail.VerdictText = "Not enough data to score this shot yet"
	}

	if metrics.HasDose && metrics.HasYield {
		detail.HasDoseYield = true
		detail.DoseYield = fmt.Sprintf("%.1f g → %.1f g", metrics.DoseG, metrics.YieldG)
	}
	if metrics.HasRatio {
		detail.HasRatio = true
		detail.Ratio = fmt.Sprintf("1:%.1f", metrics.Ratio)
	}
	if metrics.HasEY {
		detail.EY = fmt.Sprintf("EY %.1f %%", metrics.EY)
	}
	if dur, ok := shot["duration"].(int64); ok {
		detail.Duration = formatDuration(dur)
	}
	if metrics.HasPhases {
		detail.PhasesSub = fmt.Sprintf("Preinfusion %s · Extraction %s",
			formatSecondsMMSS(metrics.PreinfusionSecs), formatSecondsMMSS(metrics.ExtractionSecs))
	}
	detail.Channeling = metrics.Channeling

	ann, _ := shot["annotation"].(map[string]any)
	if ann == nil {
		ann = map[string]any{}
	}
	detail.Coffee = annotationString(ann, "coffee")
	detail.GrinderLabel = grinderGrindLabel(annotationString(ann, "grinder"), annotationString(ann, "grindSetting"))
	detail.Rating = annotationInt(ann, "rating")

	if imgExt, _ := shot["image"].(string); imgExt != "" {
		detail.HasImage = true
		detail.ImageURL = fmt.Sprintf("api/shots/%d/image", detail.ID)
	}

	detail.HasChart = hasChartData(shot)

	return detail
}
