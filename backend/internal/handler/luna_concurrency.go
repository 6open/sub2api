package handler

import (
	"math"
	"strings"
)

const (
	lunaModelName       = "gpt-5.6-luna"
	lunaUserConcurrency = 2
)

func isLunaConcurrencyLimitedModel(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), lunaModelName)
}

// Luna slots reuse the distributed Redis user-slot implementation in a
// negative-ID namespace. Database user IDs are positive, so the namespaces
// cannot collide.
func lunaConcurrencySlotID(userID int64) int64 {
	if userID <= 0 {
		return math.MinInt64
	}
	return math.MinInt64 + userID
}

func combineReleaseFuncs(releases ...func()) func() {
	return func() {
		for index := len(releases) - 1; index >= 0; index-- {
			if releases[index] != nil {
				releases[index]()
			}
		}
	}
}
