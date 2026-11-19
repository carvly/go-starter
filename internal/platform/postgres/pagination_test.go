package postgres_test

import (
	"encoding/base64"
	"testing"
	"time"

	"app.local/internal/platform/postgres"
)

func TestCursor_RoundTrip(t *testing.T) {
	at := time.Unix(0, 1_700_000_000_123_456_789)
	const want = "0199f1a2-3b4c-7d5e-8f60-718293a4b5c6"
	createdAt, id, ok, err := postgres.DecodeCursor(postgres.EncodeCursor(at, want))
	if err != nil || !ok || !createdAt.Equal(at) || id != want {
		t.Errorf("DecodeCursor = %v, %q, %v, %v", createdAt, id, ok, err)
	}
}

func TestDecodeCursor_EmptyMeansFirstPage(t *testing.T) {
	if _, _, ok, err := postgres.DecodeCursor(""); ok || err != nil {
		t.Errorf("DecodeCursor(\"\") ok = %v, err = %v, want false, nil", ok, err)
	}
}

func TestDecodeCursor_Invalid(t *testing.T) {
	notUUID := base64.RawURLEncoding.EncodeToString([]byte("1700000000|not-a-uuid"))
	for _, c := range []string{"not-a-valid-cursor!!", "bm9waXBl", notUUID} {
		if _, _, _, err := postgres.DecodeCursor(c); err == nil {
			t.Errorf("DecodeCursor(%q) 应该返回错误", c)
		}
	}
}
