package controlapi

import (
	"testing"
	"time"
)

func TestSourceRefreshDueUsesLastAttemptAndInterval(t *testing.T) {
	base := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	source := Source{
		Origin:                "https://example.com/profile",
		AutoUpdate:            true,
		UpdateIntervalMinutes: 360,
		ImportedAt:            base,
	}
	if sourceRefreshDue(source, base.Add(5*time.Hour)) {
		t.Fatal("source became due before the configured interval")
	}
	if !sourceRefreshDue(source, base.Add(6*time.Hour)) {
		t.Fatal("source should be due at the configured interval")
	}
	attempt := base.Add(5 * time.Hour)
	source.LastRefreshAttemptAt = &attempt
	if sourceRefreshDue(source, base.Add(10*time.Hour)) {
		t.Fatal("failed/successful attempt should postpone the next scheduled retry")
	}
	if !sourceRefreshDue(source, base.Add(11*time.Hour)) {
		t.Fatal("source should be due one interval after the last attempt")
	}
}

func TestSourceRefreshDueRejectsLocalAndDisabledSources(t *testing.T) {
	now := time.Now().UTC()
	for _, source := range []Source{
		{Origin: "file:profile.yaml", AutoUpdate: true, ImportedAt: now.Add(-24 * time.Hour)},
		{Origin: "https://example.com/profile", AutoUpdate: false, ImportedAt: now.Add(-24 * time.Hour)},
	} {
		if sourceRefreshDue(source, now) {
			t.Fatalf("source should not be scheduled: %+v", source)
		}
	}
}

func TestNormalizedSourceRefreshIntervalDefaultsOutOfRange(t *testing.T) {
	if got := normalizedSourceRefreshInterval(0); got != defaultSourceRefreshIntervalMinutes {
		t.Fatalf("interval=%d", got)
	}
	if got := normalizedSourceRefreshInterval(60); got != 60 {
		t.Fatalf("interval=%d", got)
	}
	if got := normalizedSourceRefreshInterval(10081); got != defaultSourceRefreshIntervalMinutes {
		t.Fatalf("interval=%d", got)
	}
}
