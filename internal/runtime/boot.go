package runtime

import (
	"strconv"
	"strings"
	"time"

	"open-mihomo-gateway/internal/platform"
)

type BootSession struct {
	ID               string
	StartedAt        time.Time
	NetworkNamespace string
}

func (s State) BelongsToBoot(boot BootSession) bool {
	stateID := strings.TrimSpace(s.BootSessionID)
	bootID := strings.TrimSpace(boot.ID)
	bootMatches := false
	if stateID != "" && bootID != "" {
		bootMatches = strings.EqualFold(stateID, bootID)
	} else {
		if s.StartedAt.IsZero() || boot.StartedAt.IsZero() {
			return false
		}
		bootMatches = !s.StartedAt.Before(boot.StartedAt)
	}
	if !bootMatches {
		return false
	}

	// Linux boot_id belongs to the host kernel and normally survives an
	// isolated-container restart. A schema-v2 Linux network snapshot therefore
	// adds the network namespace identity to the runtime-session proof. Darwin
	// and legacy test doubles leave NetworkNamespace empty and retain boot-only
	// semantics; the real Linux CurrentBootSession fails if it cannot read it.
	if s.NetworkSnapshot != nil && s.NetworkSnapshot.Backend == platform.BackendLinuxNFTables && strings.TrimSpace(boot.NetworkNamespace) != "" {
		persistedNamespace := strings.TrimSpace(s.NetworkSnapshot.NetworkNamespace)
		if persistedNamespace == "" {
			return false
		}
		return persistedNamespace == strings.TrimSpace(boot.NetworkNamespace)
	}
	return true
}

func parseDarwinBootTime(value string) time.Time {
	marker := "sec ="
	index := strings.Index(value, marker)
	if index < 0 {
		return time.Time{}
	}
	rest := strings.TrimSpace(value[index+len(marker):])
	end := strings.IndexAny(rest, ", }")
	if end >= 0 {
		rest = rest[:end]
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}
