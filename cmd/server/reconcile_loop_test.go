package main

import (
	"os"
	"testing"
	"time"
)

func TestBookingReconcileLoopConfigDisabledWithoutSecret(t *testing.T) {
	t.Setenv("BOOKING_SYNC_SHARED_SECRET", "")
	t.Setenv("BOOKING_RECONCILE_INTERVAL_SECONDS", "30")

	enabled, interval, limit, syncState, force := bookingReconcileLoopConfig()
	if enabled {
		t.Fatal("expected disabled when secret is missing")
	}
	if interval != 0 || limit != 0 || syncState != "" || force {
		t.Fatalf("expected zero values when disabled, got interval=%v limit=%d syncState=%q force=%v", interval, limit, syncState, force)
	}
}

func TestBookingReconcileLoopConfigParsesValues(t *testing.T) {
	t.Setenv("BOOKING_SYNC_SHARED_SECRET", "secret")
	t.Setenv("BOOKING_RECONCILE_INTERVAL_SECONDS", "45")
	t.Setenv("BOOKING_RECONCILE_LIMIT", "25")

	enabled, interval, limit, syncState, force := bookingReconcileLoopConfig()
	if !enabled {
		t.Fatal("expected enabled")
	}
	if interval != 45*time.Second {
		t.Fatalf("expected 45s interval, got %v", interval)
	}
	if limit != 25 {
		t.Fatalf("expected limit 25, got %d", limit)
	}
	if syncState != "" || force {
		t.Fatalf("expected empty syncState and false force, got syncState=%q force=%v", syncState, force)
	}
}

func TestBookingReconcileLoopConfigFallsBackToDefaultLimit(t *testing.T) {
	t.Setenv("BOOKING_SYNC_SHARED_SECRET", "secret")
	t.Setenv("BOOKING_RECONCILE_INTERVAL_SECONDS", "15")
	t.Setenv("BOOKING_RECONCILE_LIMIT", "bad")

	enabled, interval, limit, syncState, force := bookingReconcileLoopConfig()
	if !enabled {
		t.Fatal("expected enabled")
	}
	if interval != 15*time.Second {
		t.Fatalf("expected 15s interval, got %v", interval)
	}
	if limit != 50 {
		t.Fatalf("expected default limit 50, got %d", limit)
	}
	if syncState != "" || force {
		t.Fatalf("expected empty syncState and false force, got syncState=%q force=%v", syncState, force)
	}
}

func TestBookingReconcileLoopConfigDisabledForBadInterval(t *testing.T) {
	t.Setenv("BOOKING_SYNC_SHARED_SECRET", "secret")
	t.Setenv("BOOKING_RECONCILE_INTERVAL_SECONDS", "bad")

	enabled, interval, limit, syncState, force := bookingReconcileLoopConfig()
	if enabled {
		t.Fatal("expected disabled for bad interval")
	}
	if interval != 0 || limit != 0 || syncState != "" || force {
		t.Fatalf("expected zero values when disabled, got interval=%v limit=%d syncState=%q force=%v", interval, limit, syncState, force)
	}
}

func TestBookingReconcileLoopConfigParsesSyncStateAndForce(t *testing.T) {
	t.Setenv("BOOKING_SYNC_SHARED_SECRET", "secret")
	t.Setenv("BOOKING_RECONCILE_INTERVAL_SECONDS", "20")
	t.Setenv("BOOKING_RECONCILE_SYNC_STATE", "sync_error")
	t.Setenv("BOOKING_RECONCILE_FORCE", "true")

	enabled, interval, limit, syncState, force := bookingReconcileLoopConfig()
	if !enabled {
		t.Fatal("expected enabled")
	}
	if interval != 20*time.Second {
		t.Fatalf("expected 20s interval, got %v", interval)
	}
	if limit != 50 {
		t.Fatalf("expected default limit 50, got %d", limit)
	}
	if syncState != "sync_error" {
		t.Fatalf("expected sync_error, got %q", syncState)
	}
	if !force {
		t.Fatal("expected force true")
	}
}

func TestBookingFreshnessWindowConfigDefaultsToTwoMinutes(t *testing.T) {
	t.Setenv("BOOKING_MIRROR_FRESHNESS_SECONDS", "")
	if got := bookingFreshnessWindowConfig(); got != 2*time.Minute {
		t.Fatalf("expected 2m, got %v", got)
	}
}

func TestBookingFreshnessWindowConfigParsesOverride(t *testing.T) {
	t.Setenv("BOOKING_MIRROR_FRESHNESS_SECONDS", "90")
	if got := bookingFreshnessWindowConfig(); got != 90*time.Second {
		t.Fatalf("expected 90s, got %v", got)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
