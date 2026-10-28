//go:build dev

package auth

import (
	"net/http/httptest"
	"testing"
)

func TestDefaultVerifierIsDevIdentityInDevBuild(t *testing.T) {
	id, err := DefaultVerifier().Verify(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if id.Email != "dev@localhost" {
		t.Errorf("Email = %q, want dev@localhost", id.Email)
	}
}
