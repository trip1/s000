package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidPersonalAccessToken = errors.New("invalid personal access token")
	ErrExpiredPersonalAccessToken = errors.New("personal access token expired")
	ErrMissingPATSigningKey       = errors.New("pat signing key is required")
)

const personalAccessTokenPrefix = "s000pat"

type personalAccessTokenClaims struct {
	TokenID   string `json:"jti"`
	Subject   string `json:"sub"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
}

func CreatePersonalAccessToken(subject string, signingKey []byte, now time.Time, ttl time.Duration) (string, error) {
	tokenID := randomTokenID()
	if tokenID == "" {
		return "", ErrInvalidPersonalAccessToken
	}
	return createPersonalAccessTokenWithID(subject, signingKey, now, ttl, tokenID)
}

func createPersonalAccessTokenWithID(subject string, signingKey []byte, now time.Time, ttl time.Duration, tokenID string) (string, error) {
	subject = strings.TrimSpace(subject)
	tokenID = strings.TrimSpace(tokenID)
	if subject == "" || tokenID == "" || ttl <= 0 {
		return "", ErrInvalidCredentialInput
	}
	if len(signingKey) == 0 {
		return "", ErrMissingPATSigningKey
	}

	claims := personalAccessTokenClaims{
		TokenID:   tokenID,
		Subject:   subject,
		IssuedAt:  now.UTC().Unix(),
		ExpiresAt: now.UTC().Add(ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", ErrInvalidPersonalAccessToken
	}
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	sig := signPersonalAccessTokenPayload(payloadEncoded, signingKey)

	return personalAccessTokenPrefix + "." + payloadEncoded + "." + sig, nil
}

func VerifyPersonalAccessToken(token string, signingKey []byte, now time.Time) (string, error) {
	if len(signingKey) == 0 {
		return "", ErrMissingPATSigningKey
	}
	payloadEncoded, sigEncoded, err := splitPersonalAccessToken(token)
	if err != nil {
		return "", err
	}

	expectedSig := signPersonalAccessTokenPayload(payloadEncoded, signingKey)
	if subtle.ConstantTimeCompare([]byte(sigEncoded), []byte(expectedSig)) != 1 {
		return "", ErrInvalidPersonalAccessToken
	}

	payload, err := base64.RawURLEncoding.DecodeString(payloadEncoded)
	if err != nil {
		return "", ErrInvalidPersonalAccessToken
	}
	var claims personalAccessTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ErrInvalidPersonalAccessToken
	}
	if strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.TokenID) == "" || claims.ExpiresAt <= 0 {
		return "", ErrInvalidPersonalAccessToken
	}
	if now.UTC().Unix() >= claims.ExpiresAt {
		return "", ErrExpiredPersonalAccessToken
	}

	return strings.TrimSpace(claims.Subject), nil
}

func PersonalAccessTokenSubject(token string) string {
	payloadEncoded, _, err := splitPersonalAccessToken(token)
	if err != nil {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadEncoded)
	if err != nil {
		return ""
	}
	var claims personalAccessTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.Subject)
}

func splitPersonalAccessToken(token string) (payloadEncoded string, sigEncoded string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != personalAccessTokenPrefix || parts[1] == "" || parts[2] == "" {
		return "", "", ErrInvalidPersonalAccessToken
	}
	return parts[1], parts[2], nil
}

func signPersonalAccessTokenPayload(payloadEncoded string, signingKey []byte) string {
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write([]byte(payloadEncoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randomTokenID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
