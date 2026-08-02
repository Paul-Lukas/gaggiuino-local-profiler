// #551: shared bean consumption math — mirrors
// lib/services/LibraryService.js's computeBeanRemaining()/matching rule
// exactly (same signature, same beanId-first-with-name-fallback precedence,
// same double-round pattern) so backend and frontend can never drift apart
// again. `doseRows` here is any array of { coffee, beanId, dose, timestamp }
// — the backend gets those from ShotRepository.getAnnotatedDoses() (SQL),
// the frontend maps them off S.shots (see public-src/views/library.js).

// A dose row's beanId, when it still resolves to SOME currently-existing
// bean (checked against `allBeans`), is trusted exclusively — that dose
// genuinely belongs to whichever bean the id points at, even if this bean's
// name happens to coincide. Only when the row's beanId is null, or points
// at a bean that no longer exists anywhere (deleted), does it fall back to
// name matching against `bean`. This is what lets a bean deleted and
// reimported under the same name recover its own consumption history,
// while never misattributing a dose that legitimately belongs to a
// different, still-existing bean that happens to share a name (#456).
export function matchesBean(doseRow, bean, idExists) {
  const beanId = doseRow.beanId;
  return beanId != null && idExists.has(beanId)
    ? beanId === bean.id
    : String(doseRow.coffee || '').toLowerCase() === String(bean.name || '').toLowerCase();
}

// Sums matching dose rows for `bean`, optionally only those at/after
// `sinceMs` (epoch ms) — used both for a bag-scoped total (activeBag's
// openedAt) and an unscoped lifetime total (sinceMs left at its default 0).
export function sumConsumedDoses(bean, doseRows, allBeans, sinceMs = 0) {
  const idExists = new Set((allBeans || []).map(b => b.id));
  return (doseRows || []).reduce((sum, r) => {
    const d = parseFloat(r.dose);
    if (!d) return sum;
    if (!matchesBean(r, bean, idExists)) return sum;
    if (sinceMs && r.timestamp * 1000 < sinceMs) return sum;
    return sum + d;
  }, 0);
}

// Remaining grams for a stock-tracked bean — consumed = sum of annotated
// doses of shots matching this bean and belonging to the active bag; without
// bags, all matching shots count. Returns null when stock is untracked
// (mirrors the backend's `bean.stock_g > 0` guard).
export function computeBeanRemaining(bean, doseRows, allBeans) {
  if (!(bean.stock_g > 0)) return null;
  const bags      = Array.isArray(bean.bags) ? bean.bags : [];
  const activeBag = bags.length ? bags[bags.length - 1] : null;
  const idExists  = new Set((allBeans || []).map(b => b.id));
  const consumed  = (doseRows || []).reduce((sum, r) => {
    const d = parseFloat(r.dose);
    if (!d) return sum;
    if (!matchesBean(r, bean, idExists)) return sum;
    if (activeBag) {
      // Which bag was active when this shot was pulled — same "bag active
      // at shot time" resolution used for roast-date/frozen-portion lookups
      // (shots/utils.js, annotation.js). A dose that predates every
      // recorded bag (bean/bag added to the library only after the shot was
      // already pulled, then assigned to it retroactively) still belongs to
      // the oldest bag on record — there was nothing else it could have
      // come from, so it must not be silently dropped from the sum.
      const shotMs = r.timestamp * 1000;
      const bagAtShotTime = bags
        .filter(b => (b.openedAt || 0) <= shotMs)
        .sort((a, b) => b.openedAt - a.openedAt)[0] || bags[0];
      if (bagAtShotTime !== activeBag) return sum;
    }
    return sum + d;
  }, 0);
  return Math.round(bean.stock_g - Math.round(consumed));
}
