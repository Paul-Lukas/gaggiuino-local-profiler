package system

import (
	"encoding/json"
	"os"
	"strconv"
)

// This mirrors internal/orders/options.go's isOrdersEnabled(): a narrow,
// single-field read of /data/options.json (written by the Supervisor),
// rather than a full loadOptions() facade — see that file's doc comment
// for the trade-off reasoning, which applies identically here.

// loadPreheatMinutes ports `Math.max(1, parseInt(opts.preheat_time) || 20)`,
// used by buildPreheatResponse/_checkReadyByPreheat/_checkPreheatNotify.
// Falls back to GLP_PREHEAT_TIME (#764, standalone Docker with no
// Supervisor) when options.json doesn't exist/parse, then to 20, matching
// loadOptions()'s own fallback chain.
func loadPreheatMinutes() int {
	if data, err := os.ReadFile(defaultOptionsFile); err == nil {
		var opts struct {
			PreheatTime json.Number `json:"preheat_time"`
		}
		if err := json.Unmarshal(data, &opts); err == nil {
			if n, err := opts.PreheatTime.Int64(); err == nil && n > 0 {
				return int(n)
			}
		}
		return 20
	}
	if n, err := strconv.Atoi(os.Getenv("GLP_PREHEAT_TIME")); err == nil && n > 0 {
		return n
	}
	return 20
}
