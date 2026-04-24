package auth

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxPresignExpiry = 7 * 24 * time.Hour

// PresignOptions controls SigV4 presigned URL generation.
type PresignOptions struct {
	Now      func() time.Time
	Region   string
	Service  string
	Terminal string
	Expires  time.Duration
}

// PresignRequest applies SigV4 query parameters and signature to req.
func PresignRequest(req *http.Request, accessKeyID, secretAccessKey string, opts PresignOptions) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("request is required")
	}
	if strings.TrimSpace(req.Method) == "" {
		return fmt.Errorf("request method is required")
	}
	if strings.TrimSpace(accessKeyID) == "" {
		return fmt.Errorf("access key id is required")
	}
	if strings.TrimSpace(secretAccessKey) == "" {
		return fmt.Errorf("secret access key is required")
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	expires := opts.Expires
	if expires == 0 {
		expires = 15 * time.Minute
	}
	if expires <= 0 || expires > maxPresignExpiry {
		return fmt.Errorf("expires must be between 1s and %s", maxPresignExpiry)
	}
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		region = "us-east-1"
	}
	service := strings.TrimSpace(opts.Service)
	if service == "" {
		service = "s3"
	}
	terminal := strings.TrimSpace(opts.Terminal)
	if terminal == "" {
		terminal = "aws4_request"
	}

	signedAt := now().UTC()
	date := signedAt.Format("20060102")
	amzDate := signedAt.Format("20060102T150405Z")
	scope := date + "/" + region + "/" + service + "/" + terminal

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", accessKeyID+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(expires/time.Second)))
	q.Set("X-Amz-SignedHeaders", "host")
	q.Del("X-Amz-Signature")
	req.URL.RawQuery = q.Encode()

	canonicalRequest, err := buildCanonicalRequest(req, []string{"host"}, true)
	if err != nil {
		return err
	}
	stringToSign := buildStringToSign(signedAt, scope, canonicalRequest)
	signature := signString(secretAccessKey, date, region, service, terminal, stringToSign)

	q = req.URL.Query()
	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()
	return nil
}
