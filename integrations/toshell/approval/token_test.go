package approval

import (
	"errors"
	"testing"
	"time"
)

func TestTokenRoundTripAndBinding(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	claims := Claims{OperationID: 9, Stage: 1, ParamsHash: "abc", TaskID: 4, SessionID: "s", BindingID: 2, ExpiresUnix: time.Now().Add(time.Minute).Unix()}
	token, err := Sign(secret, claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(secret, token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !Matches(got, 9, 1, "abc", 4, "s", 2) {
		t.Fatalf("claims = %+v", got)
	}
	if Matches(got, 9, 1, "different", 4, "s", 2) {
		t.Fatal("parameter hash must bind the token")
	}
}

func TestTokenRejectsTamperAndExpiry(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	claims := Claims{OperationID: 9, Stage: 1, ParamsHash: "abc", TaskID: 4, SessionID: "s", BindingID: 2, ExpiresUnix: time.Now().Add(-time.Second).Unix()}
	token, err := Sign(secret, claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(secret, token, time.Now()); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired token err = %v", err)
	}
	bad := "A" + token[1:]
	if _, err := Verify(secret, bad, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered token err = %v", err)
	}
}
