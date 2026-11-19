package secrand_test

import (
	"encoding/hex"
	"testing"

	"app.local/internal/kit/secrand"
)

func TestRandomAndHash(t *testing.T) {
	if got := len(secrand.Hex(24)); got != 48 {
		t.Fatalf("Hex length = %d, want 48", got)
	}
	if _, err := hex.DecodeString(secrand.Hex(24)); err != nil {
		t.Fatal(err)
	}
	first, second := secrand.URL(32), secrand.URL(32)
	if first == second {
		t.Fatal("两次随机值相同")
	}
	if got := secrand.Hash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("Hash = %q", got)
	}
}
