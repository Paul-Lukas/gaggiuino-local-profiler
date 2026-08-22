// shot-chart.js is Phase B's (#901) shot-detail chart: a standalone
// vanilla-JS module (same pattern as static/live.js — see that file's own
// doc comment for the full "why a standalone module instead of a
// server-rendered fragment" reasoning, which applies here for a different
// reason: Chart.js itself needs a live DOM canvas + JS object to draw
// into, not something templ can emit as static markup) that renders a
// STATIC post-shot pressure/flow/weight/temperature chart, not a live SSE
// stream — the port of public-src/views/shots/index.js's updateView()
// main-chart datasets (the non-compare-mode subset: this shot's own
// pressure/flow/weightFlow/weight/temp + their target curves), restyled
// onto the same vendored Chart.js UMD build live.js already uses
// (static/vendor/chart-4.5.1.umd.min.js — see vendor/NOTICE.md).
//
// Data source: GET /api/shots/{id} (internal/shots/handlers.go's getShot)
// — already returns a shot's full datapoints/annotation/score, the exact
// same endpoint the JSON API's own consumers use, not a Go-only endpoint
// invented for this page. This module fetches its own token the same way
// static/live.js does (rather than reaching into glp-token.js's own
// module-private state), keeping this file a genuinely standalone module.
//
// Render trigger: templates/shots.templ's canvas
// (id="shotDetailChart", data-shot-id="<id>") exists either in GET /shots'
// own initial server-render, or in a GET /shots/{id} htmx fragment swapped
// into #shot-detail by a compact-row click — htmx does NOT execute a new
// <script src> for swapped-in content, so this module (loaded once, at the
// page level, like live.js) listens for htmx's own `htmx:afterSwap` event
// on #shot-detail and re-renders for whatever shot-id the swapped-in
// canvas now carries, in addition to rendering once on its own initial
// load for the server-rendered case.
/* global Chart */
(function () {
	"use strict";

	var glpToken = null;
	var chart = null;

	function fetchToken() {
		return fetch("api/token")
			.then(function (r) {
				return r.ok ? r.json() : null;
			})
			.then(function (body) {
				if (body && body.apiToken) glpToken = body.apiToken;
			})
			.catch(function () {
				// Network error, or expose_api_port=false with no Ingress:
				// stay tokenless — the api/shots/{id} fetch below then 401s,
				// same failure mode static/live.js's own fetchToken already
				// documents.
			});
	}

	function authedFetch(url) {
		var headers = glpToken ? { "X-GLP-Token": glpToken } : {};
		return fetch(url, { headers: headers });
	}

	function mapToXY(times, values) {
		var out = [];
		if (!times || !values) return out;
		for (var i = 0; i < times.length; i++) {
			if (values[i] == null) continue;
			out.push({ x: times[i] / 10, y: values[i] / 10 });
		}
		return out;
	}

	function maxOf(arr) {
		var m = 0;
		for (var i = 0; i < arr.length; i++) {
			if (arr[i] > m) m = arr[i];
		}
		return m;
	}

	function renderChart(canvas, shot) {
		if (chart) {
			chart.destroy();
			chart = null;
		}
		var dp = (shot && shot.datapoints) || {};
		var times = dp.timeInShot || [];
		if (!times.length) return; // shots_detail.templ's HasChart gate already hides the canvas for this case; belt-and-suspenders.

		var maxTime = times[times.length - 1] / 10;
		var maxTemp = Math.ceil(maxOf(dp.temperature || []) / 10 + 5) || 100;

		chart = new Chart(canvas, {
			type: "line",
			data: {
				datasets: [
					{ label: "Pressure", data: mapToXY(times, dp.pressure), yAxisID: "y", borderWidth: 2.5, tension: 0.1, borderColor: "#3498db", backgroundColor: "transparent", pointStyle: false },
					{ label: "Flow", data: mapToXY(times, dp.pumpFlow), yAxisID: "y", borderWidth: 2, tension: 0.1, borderColor: "#f39c12", backgroundColor: "transparent", pointStyle: false },
					{ label: "Weight flow", data: mapToXY(times, dp.weightFlow), yAxisID: "y", borderWidth: 2, tension: 0.1, borderColor: "#9b59b6", backgroundColor: "transparent", pointStyle: false },
					{ label: "Weight", data: mapToXY(times, dp.shotWeight || dp.weight), yAxisID: "y1", borderWidth: 2, tension: 0.1, borderColor: "#2ecc71", backgroundColor: "transparent", pointStyle: false },
					{ label: "Temperature", data: mapToXY(times, dp.temperature), yAxisID: "y1", borderWidth: 2.5, tension: 0.1, borderColor: "#e74c3c", backgroundColor: "transparent", pointStyle: false },
					{ label: "Target pressure", data: mapToXY(times, dp.targetPressure), yAxisID: "y", borderDash: [5, 5], borderWidth: 1.5, tension: 0.1, borderColor: "#3498db", backgroundColor: "transparent", pointStyle: false },
					{ label: "Target flow", data: mapToXY(times, dp.targetPumpFlow), yAxisID: "y", borderDash: [5, 5], borderWidth: 1.5, tension: 0.1, borderColor: "#f39c12", backgroundColor: "transparent", pointStyle: false },
					{ label: "Target temperature", data: mapToXY(times, dp.targetTemperature), yAxisID: "y1", borderDash: [5, 5], borderWidth: 1.5, tension: 0.1, borderColor: "#e74c3c", backgroundColor: "transparent", pointStyle: false }
				]
			},
			options: {
				responsive: true,
				maintainAspectRatio: false,
				animation: false,
				interaction: { mode: "index", intersect: false },
				plugins: {
					legend: { display: true, position: "bottom", labels: { color: "#a4a9ad", font: { size: 11 }, boxWidth: 12, padding: 8 } },
					tooltip: {
						callbacks: {
							title: function (ctx) {
								return "Time: " + ctx[0].parsed.x.toFixed(1) + "s";
							}
						}
					}
				},
				scales: {
					x: { type: "linear", min: 0, max: Math.max(maxTime, 5), ticks: { color: "#93989c" }, grid: { color: "#2b2f33" } },
					y: { type: "linear", position: "left", min: 0, max: 12, ticks: { color: "#93989c" }, grid: { color: "#2b2f33" } },
					y1: { type: "linear", position: "right", min: 0, max: maxTemp, ticks: { color: "#93989c" }, grid: { drawOnChartArea: false } }
				}
			}
		});
	}

	function renderForCanvas(canvas) {
		var id = canvas.getAttribute("data-shot-id");
		if (!id) return;
		authedFetch("api/shots/" + encodeURIComponent(id))
			.then(function (r) {
				return r.ok ? r.json() : null;
			})
			.then(function (shot) {
				if (shot) renderChart(canvas, shot);
			})
			.catch(function () {
				/* fetch failure: canvas just stays empty, same as live.js's own network-error handling */
			});
	}

	function renderCurrent() {
		var canvas = document.getElementById("shotDetailChart");
		if (canvas && typeof Chart !== "undefined") renderForCanvas(canvas);
	}

	fetchToken().then(renderCurrent);

	document.body.addEventListener("htmx:afterSwap", function (evt) {
		if (evt.detail.target && evt.detail.target.id === "shot-detail") renderCurrent();
	});
})();
