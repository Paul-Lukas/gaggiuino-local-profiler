// glp-token.js wires the API token into every htmx write request this and
// future Phase-2 pages issue (#901 follow-up). Loaded once, globally, from
// templates/layout.templ's <head> — no per-page/per-button wiring needed.
//
// Mechanism (mirrors public-src/api.js's initToken()/apiFetch() pattern
// for the existing SPA, deliberately, rather than inventing a second
// scheme): fetch the token from the already-public GET /api/token (see
// go/internal/system/handlers.go's getToken) once this script runs, then
// attach it as the X-GLP-Token header — the same header
// internal/auth.RequireToken checks for the JSON API — to every htmx
// request via htmx's htmx:configRequest event, which any script may
// listen for without htmx itself needing to know about auth.
//
// Why fetch-and-attach instead of an SSR meta tag baked into the page:
// GET /shots (this script's own host page) is deliberately unauthenticated
// per internal/web/doc.go's "Auth model" section (Ingress-trust / static-
// asset carve-out) — anyone who can load that page can load this script
// too. But GET /api/token is *already* exactly as reachable to that same
// caller: also public by default, rate-limited, and gated only by
// expose_api_port for a non-Ingress direct-port caller (see getToken's own
// doc comment) — the identical trust boundary GET /shots itself sits
// behind. Fetching client-side therefore introduces no exposure beyond
// what api.js's initToken() already grants the SPA today; it just reuses
// that existing public endpoint instead of a second, redundant channel.
// Whatever expose_api_port/Ingress denies to GET /api/token it equally
// denies here: fetchToken() below then leaves token null, the
// htmx:configRequest listener attaches nothing, and the write request
// 401s exactly like a standalone-mode session with no token today — not a
// new failure mode, the same one GET /api/token's own doc comment
// describes for the SPA.
(function () {
	var token = null;

	function fetchToken() {
		var headers = token ? { "X-GLP-Token": token } : {};
		fetch("/api/token", { headers: headers })
			.then(function (r) {
				return r.ok ? r.json() : null;
			})
			.then(function (body) {
				if (body && body.apiToken) token = body.apiToken;
			})
			.catch(function () {
				// Network error, or expose_api_port=false with no Ingress
				// (getToken's 403): token stays null, matching
				// api.js's initToken() leaving S.glpToken empty in the
				// same situation.
			});
	}

	document.body.addEventListener("htmx:configRequest", function (evt) {
		if (token) evt.detail.headers["X-GLP-Token"] = token;
	});

	fetchToken();
})();
