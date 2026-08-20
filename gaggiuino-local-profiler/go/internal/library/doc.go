// Package library is the Go port of the coffee library domain (Phase 1d,
// issue #901): routes/library/{index,beans,grinders,baskets,puckscreens,
// milks,recipes,scan}.js's REST endpoints, the subset of
// lib/services/LibraryService.js's methods those routes actually call, and
// lib/repositories/LibraryRepository.js's getLibrary()/saveLibrary() DB
// access — the largest and most subdivided REST domain in this rewrite, per
// go/README.md's Status section.
//
// File layout mirrors the Node source it ports:
//
//	model.go               shared Entity/Library types + JS-semantics helpers
//	                        (parseInt/parseFloat coercion, trim/slice, etc.)
//	repository.go           lib/repositories/LibraryRepository.js's
//	                         getLibrary()/saveLibrary() (the `library` table)
//	sanitize.go              lib/sanitize-bean.js's individual field sanitizers
//	image.go                 lib/services/ImageService.js (full — including the
//	                          URL-fetch half shots/image.go doesn't need)
//	ssrf.go                  lib/ssrf-guard.js's assertPublicHost
//	ratelimit.go             lib/helpers.js's rateLimit(key, maxPerMinute)
//	service.go               the LibraryService.js methods this phase calls
//	                          (getBeansInfo/computeGrinderWearStats/
//	                          upsertKnownGrindSetting/setBeanImage)
//	handlers.go              routes/library/index.js + shared handler plumbing
//	handlers_beans.go        routes/library/beans.js
//	handlers_grinders.go     routes/library/grinders.js
//	handlers_baskets.go      routes/library/baskets.js
//	handlers_puckscreens.go  routes/library/puckscreens.js
//	handlers_milks.go        routes/library/milks.js
//	handlers_recipes.go      routes/library/recipes.js
//	scan.go                  routes/library/scan.js
//
// # Deliberately not ported in this phase
//
// Per the Phase 1d task's explicit scope, the five LibraryService.js
// migrateX() methods (migrateImportedNotes/migrateNotesToFlavors/
// migrateOriginToOrigins/migrateVarietyToSpecies/migrateAnnotationBeanIds)
// are one-time startup migrations against data already migrated on every
// install this Go binary can run against — not ported, matching the task's
// instruction. None of the five turned out to be live business logic on
// inspection (all are idempotent, guarded by "already has the new field"
// checks, and only ever called once at process startup in server.js) — no
// flag needed there.
//
// Everything else deferred is a genuine cross-domain dependency on a
// package that's still a Phase 0 placeholder, each documented at its call
// site:
//
//   - LibraryService.js's getMaintenance/saveMaintenance/getMaintenanceLog/
//     addMaintenanceLogEntry/computeMaintenanceStats and
//     LibraryRepository.js's matching methods belong to the maintenance
//     domain (routes/maintenance.js -> internal/maintenance, still Phase 0).
//     The one place this package would otherwise call into them —
//     POST /api/library/grinder/:id/delete's cleanup of the deleted
//     grinder's `maintenance` table row — is flagged as a genuine (if
//     minor) behavior gap in handlers_grinders.go's deleteGrinder doc
//     comment, NOT silently dropped: this IS live, on-every-delete
//     business logic, not a one-time migration, so it doesn't qualify for
//     the migrateX() exemption above.
//   - LibraryService.js's geocodeBean (region -> map coordinates via
//     lib/geo.js, an external geocoding provider) is fire-and-forget and
//     not part of this phase's explicit scope (unlike setBeanImage's
//     ALLOWED_IMAGE_HOSTS-gated download, which the task called out by
//     name and IS ported, see image.go/service.go). A bean's `region`
//     field is still stored; `location` is just never (re)computed by the
//     Go server. Move this here once internal/geo (or equivalent) exists.
//   - LibraryService.js's checkLowStockNotify/resolveBeanForAnnotation/
//     findBeanByName/computeBeanRemaining/getActiveBeans/getActiveMilks/
//     deductMilkByName are called from the shots-annotate and orders
//     domains, not from any routes/library/*.js endpoint — out of this
//     package's scope entirely (shots' #450/#456 deferrals already cover
//     the annotate-time half; the orders half arrives with the orders
//     domain).
//   - bus.emit(EVENTS.BEAN_CHANGED, ...) (routes/library/beans.js's
//     create/update/new-bag) is not fired: the achievements domain that
//     listens for it doesn't exist in this rewrite yet. No user-visible
//     effect on the Library API surface itself — only on achievement
//     unlocks, which aren't part of this REST contract.
//
// See openapi.yaml's Library tag for the frozen response-shape contract.
// Where Node's actual runtime behavior and the OpenAPI doc disagree (e.g.
// Grinder.wear's real field names are shotsSinceBurrs/gramsSinceBurrs, not
// the doc's shots/grams — see handlers.go's withWear), this package matches
// Node's real behavior, not the doc, same rule shots/doc.go states.
package library
