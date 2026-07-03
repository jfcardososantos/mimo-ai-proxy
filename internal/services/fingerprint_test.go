/*
 * File: fingerprint_test.go
 * Project: services
 * Created: 2026-05-06
 *
 * Last Modified: Wed May 06 2026
 * Modified By: Pedro Farias
 */

package services

import (
	"flip-ai/internal/models"
	"testing"
)

func TestGenerateFingerprint(t *testing.T) {
	first := GenerateFingerprint([]models.Message{{Role: "user", Content: "hello"}})
	second := GenerateFingerprint([]models.Message{
		{Role: "system", Content: "you are a helpful assistant"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	})
	if first == "" || second == "" {
		t.Fatal("expected non-empty fingerprints")
	}
	if first != second {
		t.Fatalf("expected stable fingerprint from first user message, got %q and %q", first, second)
	}

	if got := GenerateFingerprint([]models.Message{}); got != "" {
		t.Fatalf("expected empty fingerprint for empty messages, got %q", got)
	}
}

func TestBuildAuthAcceptsCookieStyleValues(t *testing.T) {
	auth, err := buildAuth(
		`serviceToken="token-123"`,
		`userId=456`,
		`xiaomichatbot_ph="ph-789"`,
		"",
	)
	if err != nil {
		t.Fatalf("buildAuth() returned error: %v", err)
	}

	if auth.Token != "token-123" {
		t.Fatalf("unexpected token: %q", auth.Token)
	}
	if auth.UserID != "456" {
		t.Fatalf("unexpected user id: %q", auth.UserID)
	}
	if auth.Ph != "ph-789" {
		t.Fatalf("unexpected ph: %q", auth.Ph)
	}
}

func TestBuildAuthAcceptsRawCookie(t *testing.T) {
	auth, err := buildAuth(
		"",
		"",
		"",
		`serviceToken="token-abc"; userId=999; xiaomichatbot_ph="ph-xyz"; other=value`,
	)
	if err != nil {
		t.Fatalf("buildAuth() returned error: %v", err)
	}

	if auth.Token != "token-abc" {
		t.Fatalf("unexpected token: %q", auth.Token)
	}
	if auth.UserID != "999" {
		t.Fatalf("unexpected user id: %q", auth.UserID)
	}
	if auth.Ph != "ph-xyz" {
		t.Fatalf("unexpected ph: %q", auth.Ph)
	}
	if auth.Cookie == "" {
		t.Fatal("expected raw cookie to be preserved")
	}
}
