// Package shots is the Go port of the shot-history domain (Phase 1c,
// issue #901): routes/shots.js's REST endpoints (list/last/defaults/
// detail/card/annotate/trash/restore/delete/image), lib/score.js's
// scoring, lib/services/ShotService.js's annotation/trash/blocklist logic,
// and lib/repositories/ShotRepository.js's + lib/repositories/
// ShotDefaultsRepository.js's DB access — the first REST domain to go the
// full HTTP-request -> handler -> internal/db -> response path, per
// go/README.md's Status section.
//
// File layout mirrors the Node source it ports:
//
//	model.go       lib/repositories/ShotRepository.js's _hydrate()
//	score.go       lib/score.js
//	repository.go  lib/repositories/ShotRepository.js (DB access)
//	defaults.go    lib/repositories/ShotDefaultsRepository.js
//	service.go     lib/services/ShotService.js
//	validation.go  lib/validation/schemas.js (annotationSchema, shotDefaultsSchema)
//	image.go       lib/services/ImageService.js (shot-image subset only)
//	handlers.go    routes/shots.js
//
// GET /api/shots/:id/card (share-card PNG, lib/card.js) was ported in
// Phase 2f (#901) — see card.go / card_model.go / card_palette.go: an SVG
// template rasterised with a cgo-free resvg-wasm renderer, with a short
// list of deliberate cosmetic deviations documented in card.go's header
// (no frozen LEGACY_GLP layout, no shot-photo/icon.png, Go-font metrics).
//
// Still deliberately NOT ported, documented at its stub site rather than
// silently missing:
//
//   - The #450 bean-target score enhancement and the #456 low-stock
//     notification annotate() fires — both need internal/library
//     (resolveBeanForAnnotation), which is still a Phase 0 placeholder.
//     See service.go's ComputeScoreDetail and handlers.go's annotate doc
//     comments.
//
// See openapi.yaml's Shots tag for the frozen response-shape contract;
// where Node's actual runtime behavior (routes/shots.js, lib/validation/
// schemas.js) and the OpenAPI doc disagree on a status code Node itself
// returns (e.g. POST .../trash's 404 on a missing shot, absent from
// openapi.yaml but present in ShotService.js), this package matches Node's
// real behavior, not the doc.
package shots
