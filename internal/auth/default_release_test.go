//go:build !dev

package auth

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func TestDefaultVerifierRejectsInReleaseBuild(t *testing.T) {
	_, err := DefaultVerifier().Verify(httptest.NewRequest("GET", "/", nil))
	if !errors.Is(err, ErrNoIdentity) {
		t.Errorf("err = %v, want ErrNoIdentity", err)
	}
}
