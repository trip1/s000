package metadata

import (
	"context"
	"time"
)

// Bucket stores bucket metadata.
type Bucket struct {
	Name             string
	CreatedAt        time.Time
	Region           string
	VersioningStatus string
}

// ObjectVersion stores versioned object metadata.
type ObjectVersion struct {
	Bucket         string
	Key            string
	VersionID      string
	Size           int64
	ETag           string
	ChecksumSHA256 string
	ChecksumSHA1   string
	ChecksumCRC32  string
	ChecksumCRC32C string
	StoragePath    string
	Metadata       map[string]string
	DeleteMarker   bool
	CreatedAt      time.Time
}

// ListObjectsV2Options describes a backend-level S3 ListObjectsV2 page request.
type ListObjectsV2Options struct {
	Prefix     string
	Delimiter  string
	StartAfter string
	MaxKeys    int
}

// ListObjectsV2Entry is either an object or a common prefix entry.
type ListObjectsV2Entry struct {
	Value  string
	Object *ObjectVersion
}

// ListObjectsV2Result is one ordered listing page.
type ListObjectsV2Result struct {
	Entries     []ListObjectsV2Entry
	IsTruncated bool
	NextAfter   string
}

// ListObjectsV2Store is implemented by backends that can list with indexed seek pagination.
type ListObjectsV2Store interface {
	ListObjectsV2(ctx context.Context, bucket string, opts ListObjectsV2Options) (ListObjectsV2Result, error)
}

// MultipartUpload stores multipart upload metadata.
type MultipartUpload struct {
	UploadID     string
	Bucket       string
	Key          string
	SSEAlgorithm string
	InitiatedAt  time.Time
}

// MultipartPart stores multipart part metadata.
type MultipartPart struct {
	UploadID       string
	PartNumber     int
	ETag           string
	Size           int64
	ChecksumSHA256 string
	ChecksumSHA1   string
	ChecksumCRC32  string
	ChecksumCRC32C string
	StoragePath    string
	CreatedAt      time.Time
}

// CredentialRecord stores persisted credential metadata.
type CredentialRecord struct {
	AccessKeyID string
	SecretHash  string
	Status      string
	CreatedAt   time.Time
	RotatedAt   time.Time
}

// BucketWebsiteConfig stores per-bucket website hosting settings.
type BucketWebsiteConfig struct {
	Bucket              string
	IndexDocument       string
	ErrorDocument       string
	RedirectAllHost     string
	RedirectAllProtocol string
	RoutingRules        []BucketWebsiteRoutingRule
	Enabled             bool
	PublicRead          bool
}

// BucketWebsiteRoutingRule stores one website routing rule condition + redirect action.
type BucketWebsiteRoutingRule struct {
	Condition BucketWebsiteRoutingCondition
	Redirect  BucketWebsiteRedirect
}

// BucketWebsiteRoutingCondition stores website rule matching criteria.
type BucketWebsiteRoutingCondition struct {
	KeyPrefixEquals             string
	HttpErrorCodeReturnedEquals string
}

// BucketWebsiteRedirect stores website redirect behavior for one rule.
type BucketWebsiteRedirect struct {
	HostName             string
	Protocol             string
	ReplaceKeyPrefixWith string
	ReplaceKeyWith       string
	HTTPRedirectCode     string
}

// BucketCORSConfig stores per-bucket CORS settings.
type BucketCORSConfig struct {
	Bucket         string
	AllowedOrigins string
	AllowedMethods string
	AllowedHeaders string
	ExposeHeaders  string
	MaxAgeSeconds  int
	Enabled        bool
}

// BucketPolicy stores per-bucket policy document state.
type BucketPolicy struct {
	Bucket   string
	Document string
	Enabled  bool
}

// BucketPublicAccessBlock stores per-bucket public access block toggles.
type BucketPublicAccessBlock struct {
	Bucket                string
	BlockPublicACLs       bool
	IgnorePublicACLs      bool
	BlockPublicPolicy     bool
	RestrictPublicBuckets bool
}

// BucketLifecycle stores a bucket lifecycle XML document.
type BucketLifecycle struct {
	Bucket   string
	Document string
	Enabled  bool
}

// BucketNotification stores a bucket notification XML document.
type BucketNotification struct {
	Bucket   string
	Document string
	Enabled  bool
}

// BucketReplication stores a bucket replication XML document.
type BucketReplication struct {
	Bucket   string
	Document string
	Enabled  bool
}

// ObjectTagging stores an object tagging XML document.
type ObjectTagging struct {
	Bucket    string
	Key       string
	VersionID string
	Document  string
}

// ConsistencyIssue describes a metadata consistency failure.
type ConsistencyIssue struct {
	Code    string
	Message string
}

// Migration describes a schema migration unit.
type Migration struct {
	Version int
	Name    string
	UpSQL   string
}
