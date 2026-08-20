#!/usr/bin/env bash
# Phase 3a (#901) standalone wiring smoke test.
#
# Builds cmd/server, boots it standalone against a throwaway SQLite DB (no
# Node app, no real machine, no HA Supervisor), and exercises at least one
# happy-path call against every REST domain the Go server wires together in
# main.go (shots, library, machines, orders, maintenance, backup, system,
# plus auth/sse as cross-cutting concerns). It then runs three domain-
# crossing scenarios end to end:
#
#   1. Library bean -> Orders placeOrder(beanId) -> Orders active-beans
#      references the bean.
#   2. Machines create -> Library grinder create -> grinder delete -> the
#      #901 Phase 1d/1g cross-domain fix (deleting a grinder also deletes
#      its `grinder_{id}` internal/maintenance row) verified directly
#      against the SQLite file, not just through the (recomputed-on-read)
#      GET /api/maintenance response.
#   3. GET /api/backup on the first server -> POST /api/restore into a
#      second server instance pointed at a brand-new empty DB -> shots/
#      library/machines/orders counts match between the two.
#
# Exit code is non-zero if any check fails. Meant to be run locally
# (`go/scripts/smoke-test.sh` from anywhere) and reused verbatim from CI in
# a later phase — see go/README.md.
set -uo pipefail

GO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SMOKE_DIR="${GLP_SMOKE_DIR:-/tmp/glp-go-smoke}"
BIN="$SMOKE_DIR/glp-go-smoke-bin"
PORT_A="${GLP_SMOKE_PORT_A:-8199}"
PORT_B="${GLP_SMOKE_PORT_B:-8200}"
BASE_A="http://127.0.0.1:$PORT_A"
BASE_B="http://127.0.0.1:$PORT_B"

PASS=0
FAIL=0
PID_A=""
PID_B=""

ok()  { PASS=$((PASS + 1)); printf '  OK   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf '  FAIL %s\n' "$1"; }
step() { printf '\n== %s ==\n' "$1"; }

cleanup() {
	[[ -n "$PID_A" ]] && kill "$PID_A" >/dev/null 2>&1
	[[ -n "$PID_B" ]] && kill "$PID_B" >/dev/null 2>&1
	wait >/dev/null 2>&1
	rm -rf "$SMOKE_DIR"
}
trap cleanup EXIT

rm -rf "$SMOKE_DIR"
mkdir -p "$SMOKE_DIR/a" "$SMOKE_DIR/b"

step "build"
# Phase 2a (#901): cmd/server now imports internal/web, whose .templ
# sources aren't valid Go until `templ generate` writes their _templ.go
# files (git-ignored — see go/README.md's Frontend section) — required
# before this build step on a clean checkout.
if ! (cd "$GO_DIR" && go generate ./... && go build -o "$BIN" ./cmd/server) 2>"$SMOKE_DIR/build.log"; then
	bad "go generate && go build ./cmd/server"
	cat "$SMOKE_DIR/build.log"
	exit 1
fi
ok "go generate && go build ./cmd/server"

# start_server launches the binary against dbdir/{glp.db,api_token.txt} on
# port, backgrounded, with GLP_ENABLE_ORDERS=true — options.json (the
# normal source of enable_orders) never exists in this throwaway
# environment, and isOrdersEnabled() falls back to this env var exactly the
# way #764's standalone-Docker fallback intends (see
# internal/orders/options.go).
start_server() {
	local dbdir="$1" port="$2"
	(
		export GLP_DB_PATH="$dbdir/glp.db"
		export GLP_TOKEN_FILE="$dbdir/api_token.txt"
		export GLP_PORT="$port"
		export GLP_ENABLE_ORDERS=true
		exec "$BIN"
	) >"$dbdir/server.log" 2>&1 &
	echo $!
}

# wait_ready polls until the token file exists and an authenticated request
# against it succeeds, then prints the token. Fails after ~10s.
wait_ready() {
	local base="$1" token_file="$2" token=""
	for _ in $(seq 1 50); do
		if [[ -s "$token_file" ]]; then
			token=$(cat "$token_file")
			if curl -sf -o /dev/null -H "X-GLP-Token: $token" "$base/shots.json"; then
				printf '%s' "$token"
				return 0
			fi
		fi
		sleep 0.2
	done
	return 1
}

step "boot server A (standalone, fresh DB, port $PORT_A)"
PID_A=$(start_server "$SMOKE_DIR/a" "$PORT_A")
if ! TOKEN_A=$(wait_ready "$BASE_A" "$SMOKE_DIR/a/api_token.txt"); then
	bad "server A never became ready"
	cat "$SMOKE_DIR/a/server.log"
	exit 1
fi
ok "server A booted and answered an authenticated request (token acquired)"

curl_a() { curl -s -H "X-GLP-Token: $TOKEN_A" "$@"; }

step "auth"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_A/shots.json")
[[ "$code" == "401" ]] && ok "unauthenticated request rejected (401)" || bad "expected 401 for unauthenticated request, got $code"

step "domain: system (demo seed) + shots"
resp=$(curl_a -X POST "$BASE_A/api/demo/seed")
if echo "$resp" | jq -e '.ok == true and .isDemo == true' >/dev/null 2>&1; then
	ok "POST /api/demo/seed"
else
	bad "POST /api/demo/seed: $resp"
fi

shots_json=$(curl_a "$BASE_A/shots.json")
shots_count=$(echo "$shots_json" | jq 'length' 2>/dev/null || echo 0)
[[ "$shots_count" -gt 0 ]] && ok "GET /shots.json ($shots_count demo shots)" || bad "GET /shots.json: expected demo shots, got: $shots_json"

step "domain: library"
lib_json=$(curl_a "$BASE_A/api/library")
if echo "$lib_json" | jq -e '.beans | length > 0' >/dev/null 2>&1; then
	ok "GET /api/library (demo beans present)"
else
	bad "GET /api/library: expected demo beans, got: $lib_json"
fi

step "domain: machines"
machines_json=$(curl_a "$BASE_A/api/machines")
if echo "$machines_json" | jq -e 'length >= 1' >/dev/null 2>&1; then
	ok "GET /api/machines (EnsureDefaultMachine seeded the default row)"
else
	bad "GET /api/machines: $machines_json"
fi
new_machine=$(curl_a -X POST -H 'Content-Type: application/json' \
	-d '{"name":"Smoke Test Machine","type":"gaggiuino","host":""}' \
	"$BASE_A/api/machines")
new_machine_id=$(echo "$new_machine" | jq -r '.id // empty')
[[ -n "$new_machine_id" ]] && ok "POST /api/machines (id=$new_machine_id)" || bad "POST /api/machines: $new_machine"

step "domain: orders"
menu_json=$(curl_a "$BASE_A/api/orders/menu")
if echo "$menu_json" | jq -e 'length > 0' >/dev/null 2>&1; then
	ok "GET /api/orders/menu (GLP_ENABLE_ORDERS gate open)"
else
	bad "GET /api/orders/menu: $menu_json"
fi

step "domain: maintenance"
maint_json=$(curl_a "$BASE_A/api/maintenance")
if echo "$maint_json" | jq -e 'type == "object"' >/dev/null 2>&1; then
	ok "GET /api/maintenance"
else
	bad "GET /api/maintenance: $maint_json"
fi

step "domain: backup"
backup_probe=$(curl_a "$BASE_A/api/backup")
if echo "$backup_probe" | jq -e '.glp_backup == true' >/dev/null 2>&1; then
	ok "GET /api/backup (glp_backup:true legacy export)"
else
	bad "GET /api/backup: missing glp_backup flag: $(echo "$backup_probe" | head -c 300)"
fi

step "domain: system (version, machine/status, preheat)"
version_json=$(curl_a --max-time 12 "$BASE_A/api/version")
echo "$version_json" | jq -e '.current | type == "string"' >/dev/null 2>&1 \
	&& ok "GET /api/version" || bad "GET /api/version: $version_json"

status_json=$(curl_a "$BASE_A/api/machine/status")
echo "$status_json" | jq -e 'has("available")' >/dev/null 2>&1 \
	&& ok "GET /api/machine/status" || bad "GET /api/machine/status: $status_json"

preheat_json=$(curl_a "$BASE_A/api/preheat")
[[ -n "$preheat_json" ]] && echo "$preheat_json" | jq -e 'type == "object"' >/dev/null 2>&1 \
	&& ok "GET /api/preheat" || bad "GET /api/preheat: $preheat_json"

step "sse: GET /api/events"
sse_file="$SMOKE_DIR/sse_a.txt"
curl -s --max-time 3 -N "$BASE_A/api/events?token=$TOKEN_A" >"$sse_file" 2>/dev/null
line1=$(sed -n '1p' "$sse_file")
line1_len=${#line1}
# ":" + 2048 spaces (see internal/sse/sse.go's paddingBytes const).
[[ $line1_len -ge 2049 ]] && ok "padding comment present ($line1_len chars, #740 Ingress-buffer fix)" \
	|| bad "padding comment too short (${line1_len} chars): expected >= 2049"
grep -q '^event: preheat-update$' "$sse_file" && ok "primed preheat-update event" || bad "missing primed preheat-update event"
grep -q '^event: live-snapshot$' "$sse_file" && ok "primed live-snapshot event" || bad "missing primed live-snapshot event"

step "cross-domain 1: library bean -> orders placeOrder(beanId) -> orders active-beans"
bean=$(curl_a -X POST -H 'Content-Type: application/json' \
	-d '{"name":"Smoke Test Bean","stock_g":250}' \
	"$BASE_A/api/library/bean")
bean_id=$(echo "$bean" | jq -r '.id // empty')
[[ -n "$bean_id" ]] && ok "created bean id=$bean_id (stock_g=250)" || bad "POST /api/library/bean: $bean"

order=$(curl_a -X POST -H 'Content-Type: application/json' \
	-d "{\"item\":\"Espresso\",\"customer\":\"Smoke Tester\",\"beanId\":$bean_id}" \
	"$BASE_A/api/orders")
order_id=$(echo "$order" | jq -r '.id // empty')
[[ -n "$order_id" ]] && ok "placed order id=$order_id (beanId=$bean_id)" || bad "POST /api/orders: $order"

active_beans=$(curl_a "$BASE_A/api/orders/active-beans")
if echo "$active_beans" | jq -e --argjson id "$bean_id" 'any(.[]; .id == $id)' >/dev/null 2>&1; then
	ok "GET /api/orders/active-beans references bean $bean_id"
else
	bad "GET /api/orders/active-beans: bean $bean_id missing: $active_beans"
fi

step "cross-domain 2: machines -> library grinder -> delete -> maintenance row cleaned up (#901 Phase 1d/1g fix)"
grinder=$(curl_a -X POST -H 'Content-Type: application/json' \
	-d '{"name":"Smoke Test Grinder"}' \
	"$BASE_A/api/library/grinder")
grinder_id=$(echo "$grinder" | jq -r '.id // empty')
[[ -n "$grinder_id" ]] && ok "created grinder id=$grinder_id" || bad "POST /api/library/grinder: $grinder"

curl_a -X POST -H 'Content-Type: application/json' \
	-d '{"threshold_shots":150}' \
	"$BASE_A/api/maintenance/grinder_${grinder_id}/threshold" >/dev/null

row_before=$(sqlite3 "$SMOKE_DIR/a/glp.db" \
	"SELECT COUNT(*) FROM maintenance WHERE machine_id=1 AND key='grinder_${grinder_id}';")
[[ "$row_before" == "1" ]] && ok "maintenance row exists for grinder_$grinder_id before delete" \
	|| bad "expected 1 maintenance row for grinder_$grinder_id before delete, got $row_before"

curl_a -X POST "$BASE_A/api/library/grinder/${grinder_id}/delete" >/dev/null

row_after=$(sqlite3 "$SMOKE_DIR/a/glp.db" \
	"SELECT COUNT(*) FROM maintenance WHERE machine_id=1 AND key='grinder_${grinder_id}';")
[[ "$row_after" == "0" ]] && ok "maintenance row for grinder_$grinder_id gone after delete" \
	|| bad "grinder_$grinder_id maintenance row NOT cleaned up after delete (count=$row_after) -- #901 regression"

step "cross-domain 3: backup (server A) -> restore into a fresh DB (server B)"
shots_count=$(curl_a "$BASE_A/shots.json" | jq 'length')
beans_count=$(curl_a "$BASE_A/api/library" | jq '.beans | length')
grinders_count=$(curl_a "$BASE_A/api/library" | jq '.grinders | length')
machines_count=$(curl_a "$BASE_A/api/machines" | jq 'length')
orders_count=$(curl_a "$BASE_A/api/orders" | jq 'length')
curl_a "$BASE_A/api/backup" >"$SMOKE_DIR/backup.json"

PID_B=$(start_server "$SMOKE_DIR/b" "$PORT_B")
if ! TOKEN_B=$(wait_ready "$BASE_B" "$SMOKE_DIR/b/api_token.txt"); then
	bad "server B (fresh DB) never became ready"
	cat "$SMOKE_DIR/b/server.log"
else
	ok "server B booted against a brand-new empty DB"
	curl_b() { curl -s -H "X-GLP-Token: $TOKEN_B" "$@"; }

	restore_resp=$(curl_b -H 'Content-Type: application/json' \
		--data-binary @"$SMOKE_DIR/backup.json" -X POST "$BASE_B/api/restore")
	if echo "$restore_resp" | jq -e '.ok == true' >/dev/null 2>&1; then
		ok "POST /api/restore: $restore_resp"
	else
		bad "POST /api/restore failed: $restore_resp"
	fi

	shots_count_b=$(curl_b "$BASE_B/shots.json" | jq 'length')
	[[ "$shots_count_b" == "$shots_count" ]] \
		&& ok "shots restored ($shots_count_b == $shots_count)" \
		|| bad "shots count mismatch after restore: A=$shots_count B=$shots_count_b"

	lib_b=$(curl_b "$BASE_B/api/library")
	beans_count_b=$(echo "$lib_b" | jq '.beans | length')
	grinders_count_b=$(echo "$lib_b" | jq '.grinders | length')
	[[ "$beans_count_b" == "$beans_count" ]] \
		&& ok "library beans restored ($beans_count_b == $beans_count)" \
		|| bad "library beans count mismatch after restore: A=$beans_count B=$beans_count_b"
	[[ "$grinders_count_b" == "$grinders_count" ]] \
		&& ok "library grinders restored ($grinders_count_b == $grinders_count, deleted grinder stayed deleted)" \
		|| bad "library grinders count mismatch after restore: A=$grinders_count B=$grinders_count_b"

	machines_count_b=$(curl_b "$BASE_B/api/machines" | jq 'length')
	[[ "$machines_count_b" == "$machines_count" ]] \
		&& ok "machines restored ($machines_count_b == $machines_count)" \
		|| bad "machines count mismatch after restore: A=$machines_count B=$machines_count_b"

	orders_count_b=$(curl_b "$BASE_B/api/orders" | jq 'length')
	[[ "$orders_count_b" == "$orders_count" ]] \
		&& ok "orders restored ($orders_count_b == $orders_count)" \
		|| bad "orders count mismatch after restore: A=$orders_count B=$orders_count_b"
fi

step "summary"
printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]
