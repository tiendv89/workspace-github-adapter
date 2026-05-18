package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildSig computes the HMAC-SHA256 signature for a payload.
func buildSig(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookHandler_InvalidSignature verifies that requests with wrong HMAC are rejected.
func TestWebhookHandler_InvalidSignature(t *testing.T) {
	h := &serviceHandler{webhookSecret: "mysecret"}

	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/o/r"},"commits":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=badsignature")
	rec := httptest.NewRecorder()

	h.webhookHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// TestWebhookHandler_MethodNotAllowed verifies that non-POST requests are rejected.
func TestWebhookHandler_MethodNotAllowed(t *testing.T) {
	h := &serviceHandler{}
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rec := httptest.NewRecorder()
	h.webhookHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// TestWebhookHandler_NonPushEvent verifies that non-push events are ignored with 200.
func TestWebhookHandler_NonPushEvent(t *testing.T) {
	h := &serviceHandler{webhookSecret: ""}
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "pull_request")
	rec := httptest.NewRecorder()
	h.webhookHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ignored" {
		t.Errorf("expected status=ignored, got %q", resp["status"])
	}
}

// TestIsDedupeError verifies dedupe error detection.
func TestIsDedupeError_Match(t *testing.T) {
	err := &fakeError{"task already exists"}
	if !isDedupeError(err) {
		t.Error("expected dedup error to match")
	}
}

func TestIsDedupeError_NoMatch(t *testing.T) {
	err := &fakeError{"some other error"}
	if isDedupeError(err) {
		t.Error("expected non-dedup error to not match")
	}
}

func TestIsDedupeError_Nil(t *testing.T) {
	if isDedupeError(nil) {
		t.Error("expected nil error to not match")
	}
}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }
