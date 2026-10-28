//go:build !dev

package auth

// DefaultVerifier is the verifier a release build starts with: it rejects every
// request. The real Cloudflare Access verifier replaces it in issue #10.
func DefaultVerifier() Verifier { return RejectAll{} }
