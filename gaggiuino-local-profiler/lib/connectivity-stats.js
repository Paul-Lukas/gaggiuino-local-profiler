'use strict';
// #710: periodic connectivity summary for the machine host, computed from
// pollViaGaggiuinoStatus()'s own 1s-tick outcomes (lib/poll.js) -- gives a
// diagnosable "how flaky is this specific user's link" picture without
// relying on them running ping/curl by hand from the right host, which
// support has repeatedly shown doesn't happen reliably. Pure summarization
// only (records in, string out) so it's testable without mocking axios/time.
const WINDOW_MS = 60_000;

// records: [{ ok: bool, latencyMs: number|null, err: string|null }]
function summarizeConnectivity(records) {
    const total = records.length;
    if (!total) return null;

    const ok     = records.filter(r => r.ok);
    const failed = records.filter(r => !r.ok);

    const avgLatency = ok.length
        ? Math.round(ok.reduce((sum, r) => sum + r.latencyMs, 0) / ok.length)
        : null;

    const errorCounts = {};
    for (const r of failed) {
        const key = r.err || 'unknown';
        errorCounts[key] = (errorCounts[key] || 0) + 1;
    }
    const errorSummary = Object.entries(errorCounts)
        .map(([code, count]) => `${code} x${count}`)
        .join(', ');

    const parts = [`${ok.length}/${total} ok`];
    if (avgLatency != null) parts.push(`avg latency ${avgLatency}ms`);
    if (errorSummary) parts.push(`errors: ${errorSummary}`);
    return parts.join(', ');
}

module.exports = { summarizeConnectivity, WINDOW_MS };
