package serverpolicy

import (
	"fmt"
	"strconv"
	"strings"
)

// Policy is an immutable set of server IDs excluded from bot-managed access.
// Its zero value allows all servers, preserving existing deployments by default.
type Policy struct {
	disabledIDs map[int64]struct{}
}

// ParseDisabledServerIDs accepts a comma-separated list of positive database IDs.
// Reject malformed input rather than silently leaving a retired server enabled.
func ParseDisabledServerIDs(raw string) (Policy, error) {
	if strings.TrimSpace(raw) == "" {
		return Policy{}, nil
	}

	disabled := make(map[int64]struct{})
	for _, entry := range strings.Split(raw, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(entry), 10, 64)
		if err != nil || id <= 0 {
			return Policy{}, fmt.Errorf("BOT_DISABLED_SERVER_IDS must contain comma-separated positive server IDs")
		}
		disabled[id] = struct{}{}
	}
	return Policy{disabledIDs: disabled}, nil
}

func (p Policy) IsDisabled(serverID int64) bool {
	_, disabled := p.disabledIDs[serverID]
	return disabled
}
