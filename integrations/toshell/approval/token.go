// Package approval implements short-lived, parameter-bound approval tokens.
// The raw token is returned only by an authenticated operator challenge; the
// database stores its SHA-256 hash.
package approval

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid = errors.New("approval token invalid")
	ErrExpired = errors.New("approval token expired")
)

type Claims struct {
	OperationID int64  `json:"op"`
	Stage       int    `json:"stage"`
	ParamsHash  string `json:"params"`
	TaskID      int64  `json:"task"`
	SessionID   string `json:"session"`
	BindingID   int64  `json:"binding"`
	ExpiresUnix int64  `json:"exp"`
}

func Sign(secret []byte, claims Claims) (string, error) {
	if len(secret) < 32 || claims.OperationID <= 0 || claims.Stage <= 0 || claims.ParamsHash == "" || claims.ExpiresUnix <= 0 {
		return "", ErrInvalid
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + sig, nil
}

func Verify(secret []byte, token string, now time.Time) (Claims, error) {
	var claims Claims
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return claims, ErrInvalid
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(want, got) {
		return claims, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return Claims{}, ErrInvalid
	}
	if claims.OperationID <= 0 || claims.Stage <= 0 || claims.ParamsHash == "" {
		return Claims{}, ErrInvalid
	}
	if now.Unix() > claims.ExpiresUnix {
		return claims, ErrExpired
	}
	return claims, nil
}

func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func Matches(claims Claims, operationID int64, stage int, paramsHash string, taskID int64, sessionID string, bindingID int64) bool {
	return claims.OperationID == operationID &&
		claims.Stage == stage &&
		claims.ParamsHash == paramsHash &&
		claims.TaskID == taskID &&
		claims.SessionID == sessionID &&
		claims.BindingID == bindingID
}
