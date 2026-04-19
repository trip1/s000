package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrPersonalAccessTokenNotFound = errors.New("personal access token not found")

type IssuedPersonalAccessToken struct {
	ID        string
	Label     string
	Subject   string
	CreatedAt time.Time
	ExpiresAt time.Time
	Revoked   bool
	RevokedAt time.Time
}

type issuedPATRecord struct {
	IssuedPersonalAccessToken
	tokenHash string
}

type PersonalAccessTokenManager struct {
	mu         sync.RWMutex
	signingKey []byte
	now        func() time.Time
	issued     map[string]issuedPATRecord
}

func NewPersonalAccessTokenManager(signingKey []byte, now func() time.Time) *PersonalAccessTokenManager {
	if now == nil {
		now = time.Now
	}
	return &PersonalAccessTokenManager{
		signingKey: append([]byte(nil), signingKey...),
		now:        now,
		issued:     map[string]issuedPATRecord{},
	}
}

func (m *PersonalAccessTokenManager) Issue(subject string, ttl time.Duration, label string) (token string, issued IssuedPersonalAccessToken, err error) {
	now := m.now().UTC()
	tokenID := randomTokenID()
	token, err = createPersonalAccessTokenWithID(subject, m.signingKey, now, ttl, tokenID)
	if err != nil {
		return "", IssuedPersonalAccessToken{}, err
	}

	issued = IssuedPersonalAccessToken{
		ID:        tokenID,
		Label:     strings.TrimSpace(label),
		Subject:   strings.TrimSpace(subject),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}

	m.mu.Lock()
	m.issued[tokenID] = issuedPATRecord{IssuedPersonalAccessToken: issued, tokenHash: tokenFingerprint(token)}
	m.mu.Unlock()

	return token, issued, nil
}

func (m *PersonalAccessTokenManager) Verify(token string) (string, error) {
	subject, err := VerifyPersonalAccessToken(token, m.signingKey, m.now().UTC())
	if err != nil {
		return "", err
	}

	hash := tokenFingerprint(token)
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, rec := range m.issued {
		if rec.tokenHash == hash {
			if rec.Revoked {
				return "", ErrInvalidPersonalAccessToken
			}
			break
		}
	}

	return subject, nil
}

func (m *PersonalAccessTokenManager) Revoke(tokenID string) error {
	tokenID = strings.TrimSpace(tokenID)
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.issued[tokenID]
	if !ok {
		return ErrPersonalAccessTokenNotFound
	}
	if rec.Revoked {
		return nil
	}
	rec.Revoked = true
	rec.RevokedAt = m.now().UTC()
	m.issued[tokenID] = rec
	return nil
}

func (m *PersonalAccessTokenManager) List() []IssuedPersonalAccessToken {
	m.mu.RLock()
	items := make([]IssuedPersonalAccessToken, 0, len(m.issued))
	for _, rec := range m.issued {
		items = append(items, rec.IssuedPersonalAccessToken)
	}
	m.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
