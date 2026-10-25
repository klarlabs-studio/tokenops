package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

// writeTipVolume prints how many tips of each kind the coach gave per day
// and how often nothing showed they were seen: the measure every cut to
// the coach's voice is judged by.
func writeTipVolume(out io.Writer, vols []coachcap.KindVolume, s coachhook.Stats) {
	if len(vols) == 0 && len(s.StoodDown) == 0 {
		return
	}
	fmt.Fprintf(out, "\nTip volume — last %d days, per day oldest to newest; unseen = nothing showed the tip was read\n", coachcap.TipVolumeDays)
	for _, v := range vols {
		rate := "unseen rate n/a (under 5 settled)"
		if r, ok := v.UnseenRate(); ok {
			rate = fmt.Sprintf("%d%% unseen", int(r*100+0.5))
		}
		fmt.Fprintf(out, "  %-14s %3d tips  [%s]  %d unseen, %d followed, %d ignored, %d open · %s\n",
			v.Kind, v.Offers, joinInts(v.PerDay), v.Unseen, v.Followed, v.Ignored, v.Open, rate)
	}
	for _, k := range sortedKeys(s.StoodDown) {
		surface := coachhook.SurfaceDescription(k)
		fmt.Fprintf(out, "  stood down %-14s %3d times: %s already says it (`verbosity verbose` still gives the tip)\n", k, s.StoodDown[k], surface)
	}
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, " ")
}
