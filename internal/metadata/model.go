package metadata

import "time"

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
	StoragePath    string
	Metadata       map[string]string
	DeleteMarker   bool
	CreatedAt      time.Time
}

// MultipartUpload stores multipart upload metadata.
type MultipartUpload struct {
	UploadID    string
	Bucket      string
	Key         string
	InitiatedAt time.Time
}

// MultipartPart stores multipart part metadata.
type MultipartPart struct {
	UploadID       string
	PartNumber     int
	ETag           string
	Size           int64
	ChecksumSHA256 string
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
	Enabled             bool
	PublicRead          bool
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
