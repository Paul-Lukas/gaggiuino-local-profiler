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

// Which bag was active when a dose (shot) was pulled — same "bag active at
// shot time" resolution used for roast-date/frozen-portion lookups
// (shots/utils.js, annotation.js). A dose that predates every recorded bag
// (bean/bag added to the library only after the shot was already pulled,
// then assigned to it retroactively) still belongs to the oldest bag on
// record — there was nothing else it could have come from, so it must not
// be silently dropped from the sum. Shared by computeBeanRemaining and
// sumConsumedDoses's bag-scoped total so the two can never resolve a dose's
// bag differently again (#788).
function resolveBagAtShotTime(bags, shotMs) {
  return bags
    .filter(b => (b.openedAt || 0) <= shotMs)
    .sort((a, b) => b.openedAt - a.openedAt)[0] || bags[0];
}

// Sums matching dose rows for `bean`. With `bags` omitted (or empty), every
// matching dose counts — an unscoped lifetime total. With `bags` given, only
// doses that resolveBagAtShotTime() attributes to the last bag in the array
// (the active bag, same convention as computeBeanRemaining) count — a
// bag-scoped total that resolves each dose's bag exactly like
// computeBeanRemaining does, instead of a flat openedAt timestamp cutoff
// that disagreed with it on doses predating the only recorded bag (#788).
export function sumConsumedDoses(bean, doseRows, allBeans, bags = null) {
  const idExists  = new Set((allBeans || []).map(b => b.id));
  const bagList   = Array.isArray(bags) && bags.length ? bags : null;
  const activeBag = bagList ? bagList[bagList.length - 1] : null;
  return (doseRows || []).reduce((sum, r) => {
    const d = parseFloat(r.dose);
    if (!d) return sum;
    if (!matchesBean(r, bean, idExists)) return sum;
    if (activeBag && resolveBagAtShotTime(bagList, r.timestamp * 1000) !== activeBag) return sum;
    return sum + d;
  }, 0);
}

// Remaining grams for a stock-tracked bean — sums per-bag clamped remainders
// across ALL bags so that unfinished older bags contribute their remaining
// stock to the total. Each bag's contribution is max(0, bag.stock_g −
// round(doses_attributed_to_bag)); the final total is rounded once. The
// active bag (last element) falls back to bean.stock_g when it has no
// explicit stock_g field (bags created before per-bag stock tracking). With
// no bags at all, all matching doses count against bean.stock_g. Returns null
// when no bag carries a positive stock_g (mirrors backend's `> 0` guard).
export function computeBeanRemaining(bean, doseRows, allBeans) {
  const bags     = Array.isArray(bean.bags) ? bean.bags : [];
  const idExists = new Set((allBeans || []).map(b => b.id));

  if (bags.length === 0) {
    if (!(bean.stock_g > 0)) return null;
    const consumed = (doseRows || []).reduce((sum, r) => {
      const d = parseFloat(r.dose);
      return d && matchesBean(r, bean, idExists) ? sum + d : sum;
    }, 0);
    return Math.round(bean.stock_g - Math.round(consumed));
  }

  let hasStock = false;
  let totalRemaining = 0;
  for (let i = 0; i < bags.length; i++) {
    const bg = bags[i];
    const rawStock = bg.stock_g ?? (i === bags.length - 1 ? bean.stock_g : null);
    const stockG = parseFloat(rawStock);
    if (!isFinite(stockG) || !(stockG > 0)) continue;
    hasStock = true;
    const bagConsumed = (doseRows || []).reduce((sum, r) => {
      const d = parseFloat(r.dose);
      if (!d || !matchesBean(r, bean, idExists)) return sum;
      return resolveBagAtShotTime(bags, r.timestamp * 1000) === bg ? sum + d : sum;
    }, 0);
    totalRemaining += Math.max(0, stockG - Math.round(bagConsumed));
  }
  return hasStock ? Math.round(totalRemaining) : null;
}

// Inverse of computeBeanRemaining (#930): the "Adjust stock" button lets a
// user enter the TOTAL remaining across all bags; this translates that back
// into the active bag's stock_g so that computeBeanRemaining() reports the
// entered value again. Formula: new_active_stock_g = desired_total_remaining
// + total_consumed_all_bags − other_bags_stock_g. With a single bag (or no
// bags) other_bags_stock_g is 0, so this reduces to the pre-multi-bag form.
export function remainingToStockG(bean, doseRows, allBeans, desiredTotalRemaining) {
  const bags = Array.isArray(bean.bags) ? bean.bags : [];
  const totalConsumed = sumConsumedDoses(bean, doseRows, allBeans);
  const otherBagsStock = bags.slice(0, -1).reduce((sum, bg) => {
    const s = parseFloat(bg.stock_g);
    return sum + (isFinite(s) && s > 0 ? s : 0);
  }, 0);
  return Math.round(desiredTotalRemaining + totalConsumed - otherBagsStock);
}
