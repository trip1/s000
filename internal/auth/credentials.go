package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	// ErrCredentialAlreadyExists indicates a duplicate access key ID.
	ErrCredentialAlreadyExists = errors.New("credential already exists")
	// ErrCredentialNotFound indicates that the access key ID was not found.
	ErrCredentialNotFound = errors.New("credential not found")
	// ErrBootstrapAlreadyCompleted indicates bootstrap was already executed.
	ErrBootstrapAlreadyCompleted = errors.New("bootstrap already completed")
	// ErrInvalidCredentialInput indicates missing access key or secret input.
	ErrInvalidCredentialInput = errors.New("invalid credential input")
)

// CredentialStatus represents whether a credential is usable for authentication.
type CredentialStatus string

const (
	// CredentialStatusActive allows authentication.
	CredentialStatusActive CredentialStatus = "active"
	// CredentialStatusDisabled denies authentication.
	CredentialStatusDisabled CredentialStatus = "disabled"
)

type credentialRecord struct {
	AccessKeyID         string
	SecretHash          string
	PreviousSecretHash  string
	PreviousSecretUntil time.Time
	Status              CredentialStatus
	CreatedAt           time.Time
	RotatedAt           time.Time

	secret         string
	previousSecret string
}

// CredentialStore manages in-memory credentials for SigV4 validation.
type CredentialStore struct {
	mu    sync.RWMutex
	now   func() time.Time
	creds map[string]credentialRecord
}

// NewCredentialStore builds a new credential store.
func NewCredentialStore(now func() time.Time) *CredentialStore {
	if now == nil {
		now = time.Now
	}
	return &CredentialStore{
		now:   now,
		creds: make(map[string]credentialRecord),
	}
}

// BootstrapAdminCredential creates the first credential once.
func (s *CredentialStore) BootstrapAdminCredential(accessKeyID string, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if accessKeyID == "" || secret == "" {
		return ErrInvalidCredentialInput
	}
	if len(s.creds) != 0 {
		return ErrBootstrapAlreadyCompleted
	}

	now := s.now().UTC()
	s.creds[accessKeyID] = credentialRecord{
		AccessKeyID: accessKeyID,
		SecretHash:  hashSecret(secret),
		Status:      CredentialStatusActive,
		CreatedAt:   now,
		secret:      secret,
	}

	return nil
}

// CreateCredential creates a new active credential.
func (s *CredentialStore) CreateCredential(accessKeyID string, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if accessKeyID == "" || secret == "" {
		return ErrInvalidCredentialInput
	}
	if _, exists := s.creds[accessKeyID]; exists {
		return ErrCredentialAlreadyExists
	}

	now := s.now().UTC()
	s.creds[accessKeyID] = credentialRecord{
		AccessKeyID: accessKeyID,
		SecretHash:  hashSecret(secret),
		Status:      CredentialStatusActive,
		CreatedAt:   now,
		secret:      secret,
	}

	return nil
}

// RotateCredential sets a new secret and keeps the prior one for overlap.
func (s *CredentialStore) RotateCredential(accessKeyID string, newSecret string, overlap time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if accessKeyID == "" || newSecret == "" {
		return ErrInvalidCredentialInput
	}

	rec, exists := s.creds[accessKeyID]
	if !exists {
		return ErrCredentialNotFound
	}

	now := s.now().UTC()
	rec.PreviousSecretHash = rec.SecretHash
	rec.previousSecret = rec.secret
	rec.PreviousSecretUntil = now.Add(overlap)
	rec.secret = newSecret
	rec.SecretHash = hashSecret(newSecret)
	rec.RotatedAt = now
	s.creds[accessKeyID] = rec

	return nil
}

// SetStatus updates a credential status.
func (s *CredentialStore) SetStatus(accessKeyID string, status CredentialStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.creds[accessKeyID]
	if !exists {
		return ErrCredentialNotFound
	}
	rec.Status = status
	s.creds[accessKeyID] = rec
	return nil
}

// VerifySecret checks whether a supplied secret matches an active credential.
func (s *CredentialStore) VerifySecret(accessKeyID string, secret string, at time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, exists := s.creds[accessKeyID]
	if !exists || rec.Status != CredentialStatusActive {
		return false
	}

	if compareSecret(secret, rec.SecretHash) {
		return true
	}
	if rec.previousSecret != "" && at.UTC().Before(rec.PreviousSecretUntil) && compareSecret(secret, rec.PreviousSecretHash) {
		return true
	}

	return false
}

func (s *CredentialStore) resolveSecrets(accessKeyID string, at time.Time) ([]string, bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, exists := s.creds[accessKeyID]
	if !exists {
		return nil, false, false
	}
	if rec.Status != CredentialStatusActive {
		return nil, true, false
	}

	secrets := []string{rec.secret}
	if rec.previousSecret != "" && at.UTC().Before(rec.PreviousSecretUntil) {
		secrets = append(secrets, rec.previousSecret)
	}

	return secrets, true, true
}

func compareSecret(secret string, expectedHash string) bool {
	h := hashSecret(secret)
	return subtle.ConstantTimeCompare([]byte(h), []byte(expectedHash)) == 1
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
