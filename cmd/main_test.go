package main

import (
	"testing"
	"time"
)

func TestCredentialExpirationIsHalfwayToJWTExpiry(t *testing.T) {
	now := time.Date(2026, time.July, 18, 10, 0, 0, 0, time.UTC)
	jwtExpiry := now.Add(time.Hour)
	want := now.Add(30 * time.Minute)

	if got := credentialExpiration(now, jwtExpiry); !got.Equal(want) {
		t.Fatalf("credentialExpiration() = %s, want %s", got, want)
	}
}
