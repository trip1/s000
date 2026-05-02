package metadata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound indicates that a metadata entity was not found.
	ErrNotFound = errors.New("metadata not found")
	// ErrConflict indicates that a metadata entity already exists.
	ErrConflict = errors.New("metadata conflict")
)

// TxStore is the metadata store surface available inside a transaction.
type TxStore interface {
	CreateBucket(ctx context.Context, bucket Bucket) error
	DeleteBucket(ctx context.Context, bucket string) error
	UpdateBucketVersioning(ctx context.Context, bucket string, status string) error
	PutObjectVersion(ctx context.Context, version ObjectVersion) error
	DeleteObject(ctx context.Context, bucket string, key string, versionID string, at time.Time) error
	CreateMultipartUpload(ctx context.Context, upload MultipartUpload) error
	DeleteMultipartUpload(ctx context.Context, uploadID string) error
	UpsertMultipartPart(ctx context.Context, part MultipartPart) error
	UpsertCredentialRecord(ctx context.Context, record CredentialRecord) error
}

// Store is the metadata backend interface used by the application.
type Store interface {
	TxStore
	GetBucket(ctx context.Context, name string) (Bucket, error)
	ListBuckets(ctx context.Context) ([]Bucket, error)
	PutBucketWebsite(ctx context.Context, cfg BucketWebsiteConfig) error
	GetBucketWebsite(ctx context.Context, bucket string) (BucketWebsiteConfig, error)
	DeleteBucketWebsite(ctx context.Context, bucket string) error
	PutBucketCORS(ctx context.Context, cfg BucketCORSConfig) error
	GetBucketCORS(ctx context.Context, bucket string) (BucketCORSConfig, error)
	DeleteBucketCORS(ctx context.Context, bucket string) error
	PutBucketPolicy(ctx context.Context, cfg BucketPolicy) error
	GetBucketPolicy(ctx context.Context, bucket string) (BucketPolicy, error)
	DeleteBucketPolicy(ctx context.Context, bucket string) error
	PutBucketPublicAccessBlock(ctx context.Context, cfg BucketPublicAccessBlock) error
	GetBucketPublicAccessBlock(ctx context.Context, bucket string) (BucketPublicAccessBlock, error)
	DeleteBucketPublicAccessBlock(ctx context.Context, bucket string) error
	PutBucketLifecycle(ctx context.Context, cfg BucketLifecycle) error
	GetBucketLifecycle(ctx context.Context, bucket string) (BucketLifecycle, error)
	DeleteBucketLifecycle(ctx context.Context, bucket string) error
	PutBucketNotification(ctx context.Context, cfg BucketNotification) error
	GetBucketNotification(ctx context.Context, bucket string) (BucketNotification, error)
	DeleteBucketNotification(ctx context.Context, bucket string) error
	PutBucketReplication(ctx context.Context, cfg BucketReplication) error
	GetBucketReplication(ctx context.Context, bucket string) (BucketReplication, error)
	DeleteBucketReplication(ctx context.Context, bucket string) error
	PutObjectTagging(ctx context.Context, cfg ObjectTagging) error
	GetObjectTagging(ctx context.Context, bucket string, key string, versionID string) (ObjectTagging, error)
	DeleteObjectTagging(ctx context.Context, bucket string, key string, versionID string) error
	ListObjects(ctx context.Context, bucket string) ([]ObjectVersion, error)
	ListObjectVersions(ctx context.Context, bucket string) ([]ObjectVersion, error)
	GetLatestObjectVersion(ctx context.Context, bucket string, key string) (ObjectVersion, error)
	GetObjectVersion(ctx context.Context, bucket string, key string, versionID string) (ObjectVersion, error)
	UpdateObjectMetadata(ctx context.Context, bucket string, key string, versionID string, metadata map[string]string) error
	DeleteObjectVersion(ctx context.Context, bucket string, key string, versionID string) (ObjectVersion, error)
	DeleteAllObjectVersions(ctx context.Context, bucket string, key string) ([]ObjectVersion, error)
	ListMultipartUploads(ctx context.Context, bucket string, prefix string) ([]MultipartUpload, error)
	GetMultipartUpload(ctx context.Context, uploadID string) (MultipartUpload, []MultipartPart, error)
	GetCredentialRecord(ctx context.Context, accessKeyID string) (CredentialRecord, error)
	RunInTx(ctx context.Context, fn func(tx TxStore) error) error
	ValidateConsistency(ctx context.Context) ([]ConsistencyIssue, error)
	RepairConsistency(ctx context.Context) (int, error)
}

// NewStore creates a metadata store for the configured backend.
func NewStore(cfg Config) (Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if cfg.NowProvider == nil {
		cfg.NowProvider = func() time.Time { return time.Now().UTC() }
	}

	if isNativeSQLBackend(cfg.Backend) && shouldUseNativeSQL(cfg) {
		return newSQLiteStore(cfg)
	}
	if requiresNativeMetadataStore(cfg) {
		return nil, fmt.Errorf("metadata backend %q does not yet have a native row-level store; use sqlite/libsql or local:// compatibility mode", cfg.Backend)
	}

	switch cfg.Backend {
	case BackendSQLite:
		return newMemoryBackedStore(cfg, "sqlite")
	case BackendLibSQL:
		return newMemoryBackedStore(cfg, "libsql")
	case BackendPostgreSQL:
		return newMemoryBackedStore(cfg, "postgresql")
	case BackendMariaDB:
		return newMemoryBackedStore(cfg, "mariadb")
	case BackendValkey:
		return newMemoryBackedStore(cfg, "valkey")
	default:
		return nil, fmt.Errorf("unsupported metadata backend %q", cfg.Backend)
	}
}

func isNativeSQLBackend(backend Backend) bool {
	return backend == BackendSQLite || backend == BackendLibSQL || backend == BackendPostgreSQL || backend == BackendMariaDB
}

func shouldUseNativeSQL(cfg Config) bool {
	if cfg.SQLDB != nil {
		return true
	}
	_, ok := persistentFilePath(cfg.Backend, cfg.DSN)
	return ok
}

func requiresNativeMetadataStore(cfg Config) bool {
	if strings.HasPrefix(strings.TrimSpace(cfg.DSN), "local://") {
		return false
	}
	return cfg.Valkey != nil
}

type objectKey struct {
	bucket string
	key    string
}

type memoryState struct {
	buckets       map[string]Bucket
	objects       map[objectKey][]ObjectVersion
	multipart     map[string]MultipartUpload
	parts         map[string]map[int]MultipartPart
	credentials   map[string]CredentialRecord
	websites      map[string]BucketWebsiteConfig
	cors          map[string]BucketCORSConfig
	policies      map[string]BucketPolicy
	publicAccess  map[string]BucketPublicAccessBlock
	lifecycles    map[string]BucketLifecycle
	notifications map[string]BucketNotification
	replications  map[string]BucketReplication
	taggings      map[objectKey]ObjectTagging
	backendLabel  string
}

type memoryBackedStore struct {
	mu        sync.RWMutex
	now       func() time.Time
	persister statePersister
	st        memoryState
}

type persistedObject struct {
	Bucket   string          `json:"bucket"`
	Key      string          `json:"key"`
	Versions []ObjectVersion `json:"versions"`
}

type persistedState struct {
	Buckets       map[string]Bucket                  `json:"buckets"`
	Objects       []persistedObject                  `json:"objects"`
	Multipart     map[string]MultipartUpload         `json:"multipart"`
	Parts         map[string]map[int]MultipartPart   `json:"parts"`
	Credentials   map[string]CredentialRecord        `json:"credentials"`
	Websites      map[string]BucketWebsiteConfig     `json:"websites"`
	CORS          map[string]BucketCORSConfig        `json:"cors"`
	Policies      map[string]BucketPolicy            `json:"policies"`
	PublicAccess  map[string]BucketPublicAccessBlock `json:"public_access"`
	Lifecycles    map[string]BucketLifecycle         `json:"lifecycles"`
	Notifications map[string]BucketNotification      `json:"notifications"`
	Replications  map[string]BucketReplication       `json:"replications"`
	Taggings      []ObjectTagging                    `json:"taggings"`
	BackendLabel  string                             `json:"backend_label"`
}

func newMemoryBackedStore(cfg Config, backendLabel string) (*memoryBackedStore, error) {
	store := &memoryBackedStore{
		now:       cfg.NowProvider,
		persister: newStatePersister(cfg),
		st:        emptyState(backendLabel),
	}
	if store.persister != nil {
		state, ok, err := store.persister.Load(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load persisted metadata state: %w", err)
		}
		if ok {
			store.applyPersistedState(state)
		}
	}
	return store, nil
}

func emptyState(backendLabel string) memoryState {
	return memoryState{
		buckets:       make(map[string]Bucket),
		objects:       make(map[objectKey][]ObjectVersion),
		multipart:     make(map[string]MultipartUpload),
		parts:         make(map[string]map[int]MultipartPart),
		credentials:   make(map[string]CredentialRecord),
		websites:      make(map[string]BucketWebsiteConfig),
		cors:          make(map[string]BucketCORSConfig),
		policies:      make(map[string]BucketPolicy),
		publicAccess:  make(map[string]BucketPublicAccessBlock),
		lifecycles:    make(map[string]BucketLifecycle),
		notifications: make(map[string]BucketNotification),
		replications:  make(map[string]BucketReplication),
		taggings:      make(map[objectKey]ObjectTagging),
		backendLabel:  backendLabel,
	}
}

// CreateBucket creates bucket metadata.
func (s *memoryBackedStore) CreateBucket(_ context.Context, bucket Bucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := createBucket(&s.st, bucket); err != nil {
		return err
	}
	return s.persistLocked()
}

// UpdateBucketVersioning updates one bucket versioning state.
func (s *memoryBackedStore) UpdateBucketVersioning(_ context.Context, bucket string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.st.buckets[bucket]
	if !ok {
		return ErrNotFound
	}
	b.VersioningStatus = status
	s.st.buckets[bucket] = b
	return s.persistLocked()
}

// DeleteBucket deletes one bucket metadata record.
func (s *memoryBackedStore) DeleteBucket(_ context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.buckets[bucket]; !ok {
		return ErrNotFound
	}
	delete(s.st.buckets, bucket)
	delete(s.st.websites, bucket)
	delete(s.st.cors, bucket)
	delete(s.st.policies, bucket)
	delete(s.st.publicAccess, bucket)
	delete(s.st.lifecycles, bucket)
	delete(s.st.notifications, bucket)
	delete(s.st.replications, bucket)
	return s.persistLocked()
}

// GetBucket fetches one bucket by name.
func (s *memoryBackedStore) GetBucket(_ context.Context, name string) (Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.st.buckets[name]
	if !ok {
		return Bucket{}, ErrNotFound
	}
	return b, nil
}

// ListBuckets returns all buckets.
func (s *memoryBackedStore) ListBuckets(_ context.Context) ([]Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Bucket, 0, len(s.st.buckets))
	for _, bucket := range s.st.buckets {
		out = append(out, bucket)
	}
	return out, nil
}

// PutBucketWebsite creates or updates website config for one bucket.
func (s *memoryBackedStore) PutBucketWebsite(_ context.Context, cfg BucketWebsiteConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.buckets[cfg.Bucket]; !ok {
		return ErrNotFound
	}
	s.st.websites[cfg.Bucket] = cfg
	return s.persistLocked()
}

// GetBucketWebsite fetches website config for one bucket.
func (s *memoryBackedStore) GetBucketWebsite(_ context.Context, bucket string) (BucketWebsiteConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.st.websites[bucket]
	if !ok {
		return BucketWebsiteConfig{}, ErrNotFound
	}
	return cfg, nil
}

// DeleteBucketWebsite removes website config for one bucket.
func (s *memoryBackedStore) DeleteBucketWebsite(_ context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.websites[bucket]; !ok {
		return ErrNotFound
	}
	delete(s.st.websites, bucket)
	return s.persistLocked()
}

// PutBucketCORS creates or updates CORS config for one bucket.
func (s *memoryBackedStore) PutBucketCORS(_ context.Context, cfg BucketCORSConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.buckets[cfg.Bucket]; !ok {
		return ErrNotFound
	}
	s.st.cors[cfg.Bucket] = cfg
	return s.persistLocked()
}

// GetBucketCORS fetches CORS config for one bucket.
func (s *memoryBackedStore) GetBucketCORS(_ context.Context, bucket string) (BucketCORSConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.st.cors[bucket]
	if !ok {
		return BucketCORSConfig{}, ErrNotFound
	}
	return cfg, nil
}

// DeleteBucketCORS removes CORS config for one bucket.
func (s *memoryBackedStore) DeleteBucketCORS(_ context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.cors[bucket]; !ok {
		return ErrNotFound
	}
	delete(s.st.cors, bucket)
	return s.persistLocked()
}

// PutBucketPolicy creates or updates policy for one bucket.
func (s *memoryBackedStore) PutBucketPolicy(_ context.Context, cfg BucketPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.buckets[cfg.Bucket]; !ok {
		return ErrNotFound
	}
	s.st.policies[cfg.Bucket] = cfg
	return s.persistLocked()
}

// GetBucketPolicy fetches policy for one bucket.
func (s *memoryBackedStore) GetBucketPolicy(_ context.Context, bucket string) (BucketPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.st.policies[bucket]
	if !ok {
		return BucketPolicy{}, ErrNotFound
	}
	return cfg, nil
}

// DeleteBucketPolicy removes policy for one bucket.
func (s *memoryBackedStore) DeleteBucketPolicy(_ context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.policies[bucket]; !ok {
		return ErrNotFound
	}
	delete(s.st.policies, bucket)
	return s.persistLocked()
}

// PutBucketPublicAccessBlock creates or updates public access block for one bucket.
func (s *memoryBackedStore) PutBucketPublicAccessBlock(_ context.Context, cfg BucketPublicAccessBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.buckets[cfg.Bucket]; !ok {
		return ErrNotFound
	}
	s.st.publicAccess[cfg.Bucket] = cfg
	return s.persistLocked()
}

// GetBucketPublicAccessBlock fetches public access block for one bucket.
func (s *memoryBackedStore) GetBucketPublicAccessBlock(_ context.Context, bucket string) (BucketPublicAccessBlock, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.st.publicAccess[bucket]
	if !ok {
		return BucketPublicAccessBlock{}, ErrNotFound
	}
	return cfg, nil
}

// DeleteBucketPublicAccessBlock removes public access block for one bucket.
func (s *memoryBackedStore) DeleteBucketPublicAccessBlock(_ context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.publicAccess[bucket]; !ok {
		return ErrNotFound
	}
	delete(s.st.publicAccess, bucket)
	return s.persistLocked()
}

func (s *memoryBackedStore) PutBucketLifecycle(_ context.Context, cfg BucketLifecycle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.buckets[cfg.Bucket]; !ok {
		return ErrNotFound
	}
	s.st.lifecycles[cfg.Bucket] = cfg
	return s.persistLocked()
}

func (s *memoryBackedStore) GetBucketLifecycle(_ context.Context, bucket string) (BucketLifecycle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.st.lifecycles[bucket]
	if !ok {
		return BucketLifecycle{}, ErrNotFound
	}
	return cfg, nil
}

func (s *memoryBackedStore) DeleteBucketLifecycle(_ context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.lifecycles[bucket]; !ok {
		return ErrNotFound
	}
	delete(s.st.lifecycles, bucket)
	return s.persistLocked()
}

func (s *memoryBackedStore) PutBucketNotification(_ context.Context, cfg BucketNotification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.buckets[cfg.Bucket]; !ok {
		return ErrNotFound
	}
	s.st.notifications[cfg.Bucket] = cfg
	return s.persistLocked()
}

func (s *memoryBackedStore) GetBucketNotification(_ context.Context, bucket string) (BucketNotification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.st.notifications[bucket]
	if !ok {
		return BucketNotification{}, ErrNotFound
	}
	return cfg, nil
}

func (s *memoryBackedStore) DeleteBucketNotification(_ context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.notifications[bucket]; !ok {
		return ErrNotFound
	}
	delete(s.st.notifications, bucket)
	return s.persistLocked()
}

func (s *memoryBackedStore) PutBucketReplication(_ context.Context, cfg BucketReplication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.buckets[cfg.Bucket]; !ok {
		return ErrNotFound
	}
	s.st.replications[cfg.Bucket] = cfg
	return s.persistLocked()
}

func (s *memoryBackedStore) GetBucketReplication(_ context.Context, bucket string) (BucketReplication, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.st.replications[bucket]
	if !ok {
		return BucketReplication{}, ErrNotFound
	}
	return cfg, nil
}

func (s *memoryBackedStore) DeleteBucketReplication(_ context.Context, bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.replications[bucket]; !ok {
		return ErrNotFound
	}
	delete(s.st.replications, bucket)
	return s.persistLocked()
}

func (s *memoryBackedStore) PutObjectTagging(_ context.Context, cfg ObjectTagging) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := objectKey{bucket: cfg.Bucket, key: cfg.Key + "\x00" + cfg.VersionID}
	s.st.taggings[k] = cfg
	return s.persistLocked()
}

func (s *memoryBackedStore) GetObjectTagging(_ context.Context, bucket string, key string, versionID string) (ObjectTagging, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.st.taggings[objectKey{bucket: bucket, key: key + "\x00" + versionID}]
	if !ok {
		return ObjectTagging{}, ErrNotFound
	}
	return cfg, nil
}

func (s *memoryBackedStore) DeleteObjectTagging(_ context.Context, bucket string, key string, versionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := objectKey{bucket: bucket, key: key + "\x00" + versionID}
	if _, ok := s.st.taggings[k]; !ok {
		return ErrNotFound
	}
	delete(s.st.taggings, k)
	return s.persistLocked()
}

// ListObjects lists latest visible versions for one bucket.
func (s *memoryBackedStore) ListObjects(_ context.Context, bucket string) ([]ObjectVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ObjectVersion, 0)
	for key, versions := range s.st.objects {
		if key.bucket != bucket || len(versions) == 0 {
			continue
		}
		latest := versions[len(versions)-1]
		if latest.DeleteMarker {
			continue
		}
		result = append(result, latest)
	}
	return result, nil
}

func (s *memoryBackedStore) ListObjectVersions(_ context.Context, bucket string) ([]ObjectVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.st.buckets[bucket]; !ok {
		return nil, ErrNotFound
	}
	result := make([]ObjectVersion, 0)
	for key, versions := range s.st.objects {
		if key.bucket != bucket {
			continue
		}
		result = append(result, versions...)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Key == result[j].Key {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].Key < result[j].Key
	})
	return result, nil
}

// PutObjectVersion inserts one object version row.
func (s *memoryBackedStore) PutObjectVersion(_ context.Context, version ObjectVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	putObjectVersion(&s.st, version)
	return s.persistLocked()
}

// GetLatestObjectVersion returns the latest version for a key.
func (s *memoryBackedStore) GetLatestObjectVersion(_ context.Context, bucket string, key string) (ObjectVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := s.st.objects[objectKey{bucket: bucket, key: key}]
	if len(versions) == 0 {
		return ObjectVersion{}, ErrNotFound
	}
	return versions[len(versions)-1], nil
}

// GetObjectVersion returns a specific version or latest when versionID is empty.
func (s *memoryBackedStore) GetObjectVersion(ctx context.Context, bucket string, key string, versionID string) (ObjectVersion, error) {
	if versionID == "" {
		return s.GetLatestObjectVersion(ctx, bucket, key)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := s.st.objects[objectKey{bucket: bucket, key: key}]
	for _, v := range versions {
		if v.VersionID == versionID {
			return v, nil
		}
	}
	return ObjectVersion{}, ErrNotFound
}

func (s *memoryBackedStore) UpdateObjectMetadata(_ context.Context, bucket string, key string, versionID string, metadata map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.st.objects[objectKey{bucket: bucket, key: key}]
	for i, v := range versions {
		if v.VersionID != versionID {
			continue
		}
		v.Metadata = copyStringMap(metadata)
		versions[i] = v
		s.st.objects[objectKey{bucket: bucket, key: key}] = versions
		return s.persistLocked()
	}
	return ErrNotFound
}

func (s *memoryBackedStore) DeleteObjectVersion(_ context.Context, bucket string, key string, versionID string) (ObjectVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := objectKey{bucket: bucket, key: key}
	versions := s.st.objects[k]
	for i, v := range versions {
		if v.VersionID != versionID {
			continue
		}
		removed := v
		versions = append(versions[:i], versions[i+1:]...)
		if len(versions) == 0 {
			delete(s.st.objects, k)
		} else {
			s.st.objects[k] = versions
		}
		if err := s.persistLocked(); err != nil {
			return ObjectVersion{}, err
		}
		return removed, nil
	}
	return ObjectVersion{}, ErrNotFound
}

// DeleteObject appends a delete marker version.
func (s *memoryBackedStore) DeleteObject(ctx context.Context, bucket string, key string, versionID string, at time.Time) error {
	return s.PutObjectVersion(ctx, ObjectVersion{
		Bucket:       bucket,
		Key:          key,
		VersionID:    versionID,
		DeleteMarker: true,
		CreatedAt:    at,
	})
}

// DeleteAllObjectVersions removes all versions for a key and returns removed versions.
func (s *memoryBackedStore) DeleteAllObjectVersions(_ context.Context, bucket string, key string) ([]ObjectVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := objectKey{bucket: bucket, key: key}
	versions := s.st.objects[k]
	if len(versions) == 0 {
		return nil, ErrNotFound
	}
	delete(s.st.objects, k)
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return append([]ObjectVersion(nil), versions...), nil
}

// CreateMultipartUpload creates multipart upload metadata.
func (s *memoryBackedStore) CreateMultipartUpload(_ context.Context, upload MultipartUpload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.st.multipart[upload.UploadID]; exists {
		return ErrConflict
	}
	s.st.multipart[upload.UploadID] = upload
	return s.persistLocked()
}

// DeleteMultipartUpload deletes multipart upload metadata and parts.
func (s *memoryBackedStore) DeleteMultipartUpload(_ context.Context, uploadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.multipart[uploadID]; !ok {
		return ErrNotFound
	}
	delete(s.st.multipart, uploadID)
	delete(s.st.parts, uploadID)
	return s.persistLocked()
}

// UpsertMultipartPart creates or updates a multipart part row.
func (s *memoryBackedStore) UpsertMultipartPart(_ context.Context, part MultipartPart) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.st.multipart[part.UploadID]; !exists {
		return ErrNotFound
	}
	if _, ok := s.st.parts[part.UploadID]; !ok {
		s.st.parts[part.UploadID] = make(map[int]MultipartPart)
	}
	s.st.parts[part.UploadID][part.PartNumber] = part
	return s.persistLocked()
}

// GetMultipartUpload returns upload metadata and sorted part list.
func (s *memoryBackedStore) GetMultipartUpload(_ context.Context, uploadID string) (MultipartUpload, []MultipartPart, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	upload, ok := s.st.multipart[uploadID]
	if !ok {
		return MultipartUpload{}, nil, ErrNotFound
	}
	partMap := s.st.parts[uploadID]
	partNumbers := make([]int, 0, len(partMap))
	for partNo := range partMap {
		partNumbers = append(partNumbers, partNo)
	}
	sort.Ints(partNumbers)
	parts := make([]MultipartPart, 0, len(partMap))
	for _, partNo := range partNumbers {
		parts = append(parts, partMap[partNo])
	}
	return upload, parts, nil
}

// ListMultipartUploads lists multipart uploads by bucket/prefix.
func (s *memoryBackedStore) ListMultipartUploads(_ context.Context, bucket string, prefix string) ([]MultipartUpload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.st.buckets[bucket]; !ok {
		return nil, ErrNotFound
	}
	out := make([]MultipartUpload, 0)
	for _, upload := range s.st.multipart {
		if upload.Bucket != bucket {
			continue
		}
		if prefix != "" && !strings.HasPrefix(upload.Key, prefix) {
			continue
		}
		out = append(out, upload)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key == out[j].Key {
			return out[i].UploadID < out[j].UploadID
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// UpsertCredentialRecord creates or updates metadata credential state.
func (s *memoryBackedStore) UpsertCredentialRecord(_ context.Context, record CredentialRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.credentials[record.AccessKeyID] = record
	return s.persistLocked()
}

// GetCredentialRecord gets one credential metadata record.
func (s *memoryBackedStore) GetCredentialRecord(_ context.Context, accessKeyID string) (CredentialRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.st.credentials[accessKeyID]
	if !ok {
		return CredentialRecord{}, ErrNotFound
	}
	return rec, nil
}

// RunInTx runs metadata operations in an atomic transaction.
func (s *memoryBackedStore) RunInTx(ctx context.Context, fn func(tx TxStore) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	clone := cloneState(s.st)
	tx := &memoryTx{st: &clone}
	if err := fn(tx); err != nil {
		return err
	}
	s.st = clone
	if err := s.persistLocked(); err != nil {
		return err
	}
	_ = ctx
	return nil
}

// ValidateConsistency returns consistency issues for current metadata.
func (s *memoryBackedStore) ValidateConsistency(_ context.Context) ([]ConsistencyIssue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return validateState(s.st), nil
}

// RepairConsistency repairs known consistency issues and returns repair count.
func (s *memoryBackedStore) RepairConsistency(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repaired := repairState(&s.st)
	if repaired == 0 {
		return 0, nil
	}
	if err := s.persistLocked(); err != nil {
		return 0, err
	}
	return repaired, nil
}

type memoryTx struct {
	st *memoryState
}

func (t *memoryTx) CreateBucket(_ context.Context, bucket Bucket) error {
	return createBucket(t.st, bucket)
}

func (t *memoryTx) DeleteBucket(_ context.Context, bucket string) error {
	if _, ok := t.st.buckets[bucket]; !ok {
		return ErrNotFound
	}
	delete(t.st.buckets, bucket)
	delete(t.st.websites, bucket)
	delete(t.st.cors, bucket)
	delete(t.st.policies, bucket)
	delete(t.st.publicAccess, bucket)
	delete(t.st.lifecycles, bucket)
	return nil
}

func (t *memoryTx) UpdateBucketVersioning(_ context.Context, bucket string, status string) error {
	b, ok := t.st.buckets[bucket]
	if !ok {
		return ErrNotFound
	}
	b.VersioningStatus = status
	t.st.buckets[bucket] = b
	return nil
}

func (t *memoryTx) PutObjectVersion(_ context.Context, version ObjectVersion) error {
	putObjectVersion(t.st, version)
	return nil
}

func (t *memoryTx) DeleteObject(ctx context.Context, bucket string, key string, versionID string, at time.Time) error {
	return t.PutObjectVersion(ctx, ObjectVersion{Bucket: bucket, Key: key, VersionID: versionID, DeleteMarker: true, CreatedAt: at})
}

func (t *memoryTx) CreateMultipartUpload(_ context.Context, upload MultipartUpload) error {
	if _, exists := t.st.multipart[upload.UploadID]; exists {
		return ErrConflict
	}
	t.st.multipart[upload.UploadID] = upload
	return nil
}

func (t *memoryTx) DeleteMultipartUpload(_ context.Context, uploadID string) error {
	if _, ok := t.st.multipart[uploadID]; !ok {
		return ErrNotFound
	}
	delete(t.st.multipart, uploadID)
	delete(t.st.parts, uploadID)
	return nil
}

func (t *memoryTx) UpsertMultipartPart(_ context.Context, part MultipartPart) error {
	if _, exists := t.st.multipart[part.UploadID]; !exists {
		return ErrNotFound
	}
	if _, ok := t.st.parts[part.UploadID]; !ok {
		t.st.parts[part.UploadID] = make(map[int]MultipartPart)
	}
	t.st.parts[part.UploadID][part.PartNumber] = part
	return nil
}

func (t *memoryTx) UpsertCredentialRecord(_ context.Context, record CredentialRecord) error {
	t.st.credentials[record.AccessKeyID] = record
	return nil
}

func createBucket(st *memoryState, bucket Bucket) error {
	if bucket.Name == "" {
		return fmt.Errorf("bucket name is required")
	}
	if _, exists := st.buckets[bucket.Name]; exists {
		return ErrConflict
	}
	st.buckets[bucket.Name] = bucket
	return nil
}

func putObjectVersion(st *memoryState, version ObjectVersion) {
	k := objectKey{bucket: version.Bucket, key: version.Key}
	if version.VersionID == "null" {
		versions := st.objects[k]
		filtered := versions[:0]
		for _, existing := range versions {
			if existing.VersionID != "null" {
				filtered = append(filtered, existing)
			}
		}
		st.objects[k] = filtered
	}
	st.objects[k] = append(st.objects[k], version)
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *memoryBackedStore) persistLocked() error {
	if s.persister == nil {
		return nil
	}
	return s.persister.Save(context.Background(), snapshotState(s.st))
}

func (s *memoryBackedStore) applyPersistedState(ps persistedState) {
	st := emptyState(s.st.backendLabel)
	if ps.Buckets != nil {
		st.buckets = ps.Buckets
	}
	if ps.Multipart != nil {
		st.multipart = ps.Multipart
	}
	if ps.Parts != nil {
		st.parts = ps.Parts
	}
	if ps.Credentials != nil {
		st.credentials = ps.Credentials
	}
	if ps.Websites != nil {
		st.websites = ps.Websites
	}
	if ps.CORS != nil {
		st.cors = ps.CORS
	}
	if ps.Policies != nil {
		st.policies = ps.Policies
	}
	if ps.PublicAccess != nil {
		st.publicAccess = ps.PublicAccess
	}
	if ps.Lifecycles != nil {
		st.lifecycles = ps.Lifecycles
	}
	if ps.Notifications != nil {
		st.notifications = ps.Notifications
	}
	if ps.Replications != nil {
		st.replications = ps.Replications
	}
	for _, tagging := range ps.Taggings {
		st.taggings[objectKey{bucket: tagging.Bucket, key: tagging.Key + "\x00" + tagging.VersionID}] = tagging
	}
	for _, po := range ps.Objects {
		k := objectKey{bucket: po.Bucket, key: po.Key}
		st.objects[k] = append([]ObjectVersion(nil), po.Versions...)
	}
	s.st = st
}

func snapshotState(st memoryState) persistedState {
	objects := make([]persistedObject, 0, len(st.objects))
	for k, versions := range st.objects {
		objects = append(objects, persistedObject{Bucket: k.bucket, Key: k.key, Versions: append([]ObjectVersion(nil), versions...)})
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].Bucket == objects[j].Bucket {
			return objects[i].Key < objects[j].Key
		}
		return objects[i].Bucket < objects[j].Bucket
	})

	taggings := make([]ObjectTagging, 0, len(st.taggings))
	for _, cfg := range st.taggings {
		taggings = append(taggings, cfg)
	}
	return persistedState{
		Buckets:       st.buckets,
		Objects:       objects,
		Multipart:     st.multipart,
		Parts:         st.parts,
		Credentials:   st.credentials,
		Websites:      st.websites,
		CORS:          st.cors,
		Policies:      st.policies,
		PublicAccess:  st.publicAccess,
		Lifecycles:    st.lifecycles,
		Notifications: st.notifications,
		Replications:  st.replications,
		Taggings:      taggings,
		BackendLabel:  st.backendLabel,
	}
}

func cloneState(in memoryState) memoryState {
	out := memoryState{
		buckets:       make(map[string]Bucket, len(in.buckets)),
		objects:       make(map[objectKey][]ObjectVersion, len(in.objects)),
		multipart:     make(map[string]MultipartUpload, len(in.multipart)),
		parts:         make(map[string]map[int]MultipartPart, len(in.parts)),
		credentials:   make(map[string]CredentialRecord, len(in.credentials)),
		websites:      make(map[string]BucketWebsiteConfig, len(in.websites)),
		cors:          make(map[string]BucketCORSConfig, len(in.cors)),
		policies:      make(map[string]BucketPolicy, len(in.policies)),
		publicAccess:  make(map[string]BucketPublicAccessBlock, len(in.publicAccess)),
		lifecycles:    make(map[string]BucketLifecycle, len(in.lifecycles)),
		notifications: make(map[string]BucketNotification, len(in.notifications)),
		replications:  make(map[string]BucketReplication, len(in.replications)),
		taggings:      make(map[objectKey]ObjectTagging, len(in.taggings)),
		backendLabel:  in.backendLabel,
	}

	for k, v := range in.buckets {
		out.buckets[k] = v
	}
	for k, versions := range in.objects {
		out.objects[k] = append([]ObjectVersion(nil), versions...)
	}
	for uploadID, upload := range in.multipart {
		out.multipart[uploadID] = upload
	}
	for uploadID, partMap := range in.parts {
		cloned := make(map[int]MultipartPart, len(partMap))
		for partNo, part := range partMap {
			cloned[partNo] = part
		}
		out.parts[uploadID] = cloned
	}
	for k, rec := range in.credentials {
		out.credentials[k] = rec
	}
	for k, cfg := range in.websites {
		out.websites[k] = cfg
	}
	for k, cfg := range in.cors {
		out.cors[k] = cfg
	}
	for k, cfg := range in.policies {
		out.policies[k] = cfg
	}
	for k, cfg := range in.publicAccess {
		out.publicAccess[k] = cfg
	}
	for k, cfg := range in.lifecycles {
		out.lifecycles[k] = cfg
	}
	for k, cfg := range in.notifications {
		out.notifications[k] = cfg
	}
	for k, cfg := range in.replications {
		out.replications[k] = cfg
	}
	for k, cfg := range in.taggings {
		out.taggings[k] = cfg
	}

	return out
}

func validateState(st memoryState) []ConsistencyIssue {
	issues := make([]ConsistencyIssue, 0)

	for key := range st.objects {
		if _, ok := st.buckets[key.bucket]; !ok {
			issues = append(issues, ConsistencyIssue{
				Code:    "orphan_object_versions",
				Message: fmt.Sprintf("object versions exist for missing bucket %q", key.bucket),
			})
		}
	}

	for uploadID, upload := range st.multipart {
		if _, ok := st.buckets[upload.Bucket]; !ok {
			issues = append(issues, ConsistencyIssue{
				Code:    "orphan_multipart_upload",
				Message: fmt.Sprintf("multipart upload %q references missing bucket %q", uploadID, upload.Bucket),
			})
		}
	}

	for bucket := range st.websites {
		if _, ok := st.buckets[bucket]; !ok {
			issues = append(issues, ConsistencyIssue{
				Code:    "orphan_website_config",
				Message: fmt.Sprintf("website configuration exists for missing bucket %q", bucket),
			})
		}
	}

	for bucket := range st.cors {
		if _, ok := st.buckets[bucket]; !ok {
			issues = append(issues, ConsistencyIssue{
				Code:    "orphan_cors_config",
				Message: fmt.Sprintf("cors configuration exists for missing bucket %q", bucket),
			})
		}
	}

	for bucket := range st.policies {
		if _, ok := st.buckets[bucket]; !ok {
			issues = append(issues, ConsistencyIssue{
				Code:    "orphan_bucket_policy",
				Message: fmt.Sprintf("bucket policy exists for missing bucket %q", bucket),
			})
		}
	}

	for bucket := range st.publicAccess {
		if _, ok := st.buckets[bucket]; !ok {
			issues = append(issues, ConsistencyIssue{
				Code:    "orphan_public_access_block",
				Message: fmt.Sprintf("public access block exists for missing bucket %q", bucket),
			})
		}
	}

	return issues
}

func repairState(st *memoryState) int {
	repaired := 0

	for key := range st.objects {
		if _, ok := st.buckets[key.bucket]; !ok {
			delete(st.objects, key)
			repaired++
		}
	}

	for uploadID, upload := range st.multipart {
		if _, ok := st.buckets[upload.Bucket]; !ok {
			delete(st.multipart, uploadID)
			delete(st.parts, uploadID)
			repaired++
		}
	}

	for bucket := range st.websites {
		if _, ok := st.buckets[bucket]; !ok {
			delete(st.websites, bucket)
			repaired++
		}
	}

	for bucket := range st.cors {
		if _, ok := st.buckets[bucket]; !ok {
			delete(st.cors, bucket)
			repaired++
		}
	}

	for bucket := range st.policies {
		if _, ok := st.buckets[bucket]; !ok {
			delete(st.policies, bucket)
			repaired++
		}
	}

	for bucket := range st.publicAccess {
		if _, ok := st.buckets[bucket]; !ok {
			delete(st.publicAccess, bucket)
			repaired++
		}
	}

	return repaired
}
