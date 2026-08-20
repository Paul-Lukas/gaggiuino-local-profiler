// Package db will hold the Go SQLite data-access layer that replaces the
// Node app's lib/db.js — schema creation and the additive migrations
// (migrateMachineColumns, migrateMachineTheme, ensureInstallId) that open
// and prepare /data/glp.db.
//
// Phase 0 placeholder only: the schema this package will open must stay
// byte-for-byte compatible with lib/db.js's — see the migration plan at
// ~/.claude/plans/folgendes-m-chte-ich-als-shimmying-hartmanis.md for the
// "no data migration, only schema compatibility" strategy this depends on.
// No implementation yet.
package db
