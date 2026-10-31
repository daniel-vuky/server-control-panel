package secret

import (
	"errors"
	"strings"
	"testing"
)

func key(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func TestSealRoundTripAndNonce(t *testing.T) {
	k := key(1)
	const plain = "1//refresh-token-value"
	a, err := Seal(k, plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	b, err := Seal(k, plain)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if a == b {
		t.Error("two seals of the same text are equal; the nonce must be random")
	}
	if !strings.HasPrefix(a, "v1:") || strings.Contains(a, plain) {
		t.Errorf("sealed = %q, want v1: prefix and no plain text", a)
	}
	for _, s := range []string{a, b} {
		got, err := Open(k, s)
		if err != nil || got != plain {
			t.Errorf("Open() = %q, %v; want %q, nil", got, err, plain)
		}
	}
	empty, err := Seal(k, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Open(k, empty); err != nil || got != "" {
		t.Errorf("empty round trip = %q, %v", got, err)
	}
}

func TestOpenWrongKeyOrTampered(t *testing.T) {
	k := key(1)
	sealed, err := Seal(k, "token")
	if err != nil {
		t.Fatal(err)
	}
	// Flip one character in the middle of the base64 body.
	i := len(sealed) / 2
	flipped := "A"
	if sealed[i] == 'A' {
		flipped = "B"
	}
	tampered := sealed[:i] + flipped + sealed[i+1:]
	cases := map[string]struct {
		key    []byte
		sealed string
	}{
		"wrong key":  {key(2), sealed},
		"tampered":   {k, tampered},
		"no prefix":  {k, strings.TrimPrefix(sealed, "v1:")},
		"other ver":  {k, "v2:" + strings.TrimPrefix(sealed, "v1:")},
		"not base64": {k, "v1:!!!"},
		"too short":  {k, "v1:AAAA"},
		"empty":      {k, ""},
		"truncated":  {k, sealed[:len(sealed)-8]},
	}
	for name, tc := range cases {
		got, err := Open(tc.key, tc.sealed)
		if !errors.Is(err, ErrBadSeal) || got != "" {
			t.Errorf("%s: Open() = %q, %v; want \"\", ErrBadSeal", name, got, err)
		}
	}
}

func TestBadKeyLength(t *testing.T) {
	if _, err := Seal(make([]byte, 16), "x"); err == nil {
		t.Error("Seal with a 16-byte key: want an error")
	}
	if _, err := Open(nil, "v1:AAAA"); err == nil {
		t.Error("Open with no key: want an error")
	}
}
