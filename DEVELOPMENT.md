# Development Stats

Generated 2026-09-17 by `scripts/dev-stats.mjs`. Re-run it any time to refresh these numbers — they are computed live from git history, not hand-maintained.

## Timeline

The GLP ecosystem (this app + 3 companion repos) has been in development since **2026-05-20** — **121 days** as of the last commit (2026-09-17).

| Repo | First commit | Last commit | Commits | AI co-authored |
|---|---|---|---|---|
| gaggiuino-local-profiler | 2026-05-20 | 2026-09-17 | 1250 | 848 (68%) |
| glp-integration | 2026-05-22 | 2026-09-14 | 175 | 77 (44%) |
| glp-lovelace-card | 2026-05-24 | 2026-09-17 | 159 | 85 (53%) |
| glp-order-card | 2026-05-25 | 2026-09-17 | 128 | 61 (48%) |
| **Combined** | **2026-05-20** | **2026-09-17** | **1712** | **1071 (63%)** |

![Commits per repo](docs/dev-stats/commits-per-repo.svg)

Combined line changes (insertions + deletions across all commits): **486.968**, of which **385.537** landed in AI-co-authored commits.

Commits without an AI co-author line are presumed human-only (manual fixes, merges, config tweaks) — not independently verified.

## Hours of development (lower-bound estimate)

Clustering each repo's commit timestamps into working sessions — commits within 2h of each other join the same session, and each session gets a 30-minute lead-in credited ahead of its first commit — gives a combined **524.0 hours** across all four repos.

| Repo | Hours (session-clustered) |
|---|---|
| gaggiuino-local-profiler | 336.2 |
| glp-integration | 70.0 |
| glp-lovelace-card | 63.5 |
| glp-order-card | 54.3 |
| **Combined** | **524.0** |

This is a **lower-bound estimate derived from git commit timestamps only**, not measured time — it undercounts real work because a long AI-agentic session (orchestration, agent dispatch, review between infrequent commits) can run for hours between commits.

## AI model breakdown (by commit co-author line)

| Model | Commits |
|---|---|
| Claude Sonnet 5 | 580 |
| Claude Sonnet 4.6 | 348 |
| Claude Opus 4.8 | 47 |
| Claude Opus 5 | 41 |
| Claude Fable 5 | 40 |
| Claude | 11 |
| DeepSeek V4 Flash | 2 |
| Claude Haiku 4.5 | 2 |

![AI model breakdown by commits](docs/dev-stats/model-breakdown.svg)

The exact co-author string varies by era as model names changed over the project's lifetime — this table groups by the literal string used in each commit, so the same underlying model released under a new name shows up as a separate row.

## Cost

Max pays a flat **$20/month** for Claude Pro, regardless of usage volume — this is the actual subscription cost, not a token-usage estimate. 5 months since the first commit (2026-05-20) works out to **$100.00** for every Claude-model commit combined, regardless of which Claude model did the work.

This assumes a continuous subscription for the whole span — it does not account for any gaps where the subscription might have lapsed.

| Model | Commits | Cost |
|---|---|---|
| DeepSeek V4 Flash | 2 | not tracked |

These models are billed per API usage, not a flat subscription, so no dollar figure is derivable from git history alone — no commit yet reports its own cost via a `Co-Authored-Cost-Usd` trailer.

---
*This file is generated. Do not hand-edit — re-run `node scripts/dev-stats.mjs` instead.*
