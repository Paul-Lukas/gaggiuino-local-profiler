// Command dialin-backtest replays a real GLP database's shot history
// chronologically through dialin.Recommend and reports how close its
// grind recommendations would have landed against what was actually dialed
// in — the Phase 1 POC/verification step for the statistical dial-in engine
// (see internal/dialin), run before any API/UI work starts.
//
// Not wired into cmd/server and not built/shipped by anything else in this
// repo — a standalone diagnostic, same spirit as cmd/gaggiuino-ws-probe.
//
// Usage:
//
//	go run ./cmd/dialin-backtest -db /path/to/a-COPY-of-your-db.sqlite
//
// internal/db.Open runs additive schema migrations on whatever file it's
// pointed at (see its own doc comment) — always point this at a copy of a
// real database, never the live one a running server also has open.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/dialin"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

func main() {
	dbPath := flag.String("db", "", "path to a COPY of a GLP SQLite database file (never the live one)")
	minPrior := flag.Int("min-prior", 3, "skip a shot's evaluation if fewer than this many earlier same-bean+grinder shots exist yet")
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "usage: dialin-backtest -db /path/to/db-copy.sqlite")
		os.Exit(1)
	}

	sqlDB, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("opening db: %v", err)
	}
	defer sqlDB.Close()

	shotsRepo := shots.NewRepository(sqlDB)
	libRepo := library.NewRepository(sqlDB)

	allShots, err := shotsRepo.FindAllExcludingTrash()
	if err != nil {
		log.Fatalf("loading shots: %v", err)
	}
	lib, err := libRepo.GetLibrary()
	if err != nil {
		log.Fatalf("loading library: %v", err)
	}

	recipesByID := make(map[int64]library.Entity, len(lib.Recipes))
	for _, r := range lib.Recipes {
		recipesByID[entityID(r)] = r
	}

	comparable := dialin.BuildComparableShots(allShots, lib)
	sort.Slice(comparable, func(i, j int) bool { return comparable[i].Timestamp < comparable[j].Timestamp })

	fmt.Printf("Loaded %d usable shots (grinder+grindSetting both set) out of %d total.\n\n", len(comparable), len(allShots))

	type methodStats struct {
		count       int
		sumAbsErr   float64
		sumSqErr    float64
		sampleSizes []int
	}
	stats := make(map[string]*methodStats)
	skippedNoPrior := 0
	skippedNoTarget := 0

	for i, cs := range comparable {
		prior := comparable[:i]
		if countSameBeanGrinder(prior, cs.BeanID, cs.GrinderName) < *minPrior {
			skippedNoPrior++
			continue
		}

		// Empty RecipeTarget is a legitimate input, not a skip condition —
		// Recommend's impliedTargetTimeS falls back to the same 30s ideal
		// band the existing heuristic implicitly assumes, so shots without
		// a linked recipe are still evaluable against that default.
		var target dialin.RecipeTarget
		if cs.RecipeID != 0 {
			if recipe, ok := recipesByID[cs.RecipeID]; ok {
				target = recipeTargetOf(recipe)
			} else {
				skippedNoTarget++
			}
		}

		rec := dialin.Recommend(cs.BeanID, cs.GrinderName, cs.BasketID, cs.PuckScreenID, cs.ProfileName, target, prior)
		if rec.Method == "none" {
			continue
		}

		st, ok := stats[rec.Method]
		if !ok {
			st = &methodStats{}
			stats[rec.Method] = st
		}
		errAbs := math.Abs(rec.GrindSetting - cs.Grind)
		st.count++
		st.sumAbsErr += errAbs
		st.sumSqErr += errAbs * errAbs
		st.sampleSizes = append(st.sampleSizes, rec.SampleSize)
	}

	fmt.Printf("Skipped (fewer than %d prior same-bean+grinder shots): %d\n", *minPrior, skippedNoPrior)
	fmt.Printf("Shots with a recipeId referencing a since-deleted recipe (target ignored): %d\n\n", skippedNoTarget)

	methods := make([]string, 0, len(stats))
	for m := range stats {
		methods = append(methods, m)
	}
	sort.Strings(methods)

	fmt.Println("Method       | Count | Mean |err| (grind units) | RMS err | Median sample size")
	fmt.Println("-------------|-------|-------------------------|---------|--------------------")
	for _, m := range methods {
		st := stats[m]
		mean := st.sumAbsErr / float64(st.count)
		rms := math.Sqrt(st.sumSqErr / float64(st.count))
		sort.Ints(st.sampleSizes)
		medSample := st.sampleSizes[len(st.sampleSizes)/2]
		fmt.Printf("%-12s | %5d | %23.2f | %7.2f | %19d\n", m, st.count, mean, rms, medSample)
	}
}

func countSameBeanGrinder(shots []dialin.ComparableShot, beanID int64, grinderName string) int {
	n := 0
	for _, s := range shots {
		if s.BeanID == beanID && s.GrinderName == grinderName {
			n++
		}
	}
	return n
}

func entityID(e library.Entity) int64 {
	switch v := e["id"].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func recipeTargetOf(r library.Entity) dialin.RecipeTarget {
	var t dialin.RecipeTarget
	if v, ok := r["targetTime_s"].(float64); ok && v > 0 {
		t.TargetTimeS = &v
	}
	if v, ok := r["targetYield_g"].(float64); ok && v > 0 {
		t.TargetYieldG = &v
	}
	if v, ok := r["targetDose_g"].(float64); ok && v > 0 {
		t.TargetDoseG = &v
	}
	return t
}
