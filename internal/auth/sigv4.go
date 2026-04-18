package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrMissingAuthenticationToken indicates no SigV4 auth input was provided.
	ErrMissingAuthenticationToken = errors.New("missing authentication token")
	// ErrInvalidAccessKeyID indicates an unknown access key ID.
	ErrInvalidAccessKeyID = errors.New("invalid access key id")
	// ErrSignatureDoesNotMatch indicates a signature mismatch.
	ErrSignatureDoesNotMatch = errors.New("signature does not match")
	// ErrRequestTimeTooSkewed indicates request timestamp drift exceeded limits.
	ErrRequestTimeTooSkewed = errors.New("request time too skewed")
	// ErrInvalidRequest indicates malformed SigV4 request data.
	ErrInvalidRequest = errors.New("invalid request")
	// ErrAccessDenied indicates a known but disabled credential.
	ErrAccessDenied = errors.New("access denied")
)

// VerifierOptions controls SigV4 verification behavior.
type VerifierOptions struct {
	Now      func() time.Time
	MaxSkew  time.Duration
	Service  string
	Terminal string
}

// Verifier validates SigV4 authorization and presigned URLs.
type Verifier struct {
	store    *CredentialStore
	now      func() time.Time
	maxSkew  time.Duration
	service  string
	terminal string
}

// NewVerifier builds a SigV4 verifier.
func NewVerifier(store *CredentialStore, opts VerifierOptions) *Verifier {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxSkew <= 0 {
		opts.MaxSkew = 15 * time.Minute
	}
	if opts.Service == "" {
		opts.Service = "s3"
	}
	if opts.Terminal == "" {
		opts.Terminal = "aws4_request"
	}

	return &Verifier{
		store:    store,
		now:      opts.Now,
		maxSkew:  opts.MaxSkew,
		service:  opts.Service,
		terminal: opts.Terminal,
	}
}

// VerifyRequest validates either Authorization-header SigV4 or presigned SigV4.
func (v *Verifier) VerifyRequest(r *http.Request) error {
	if v == nil || v.store == nil {
		return ErrAccessDenied
	}

	if r.Header.Get("Authorization") != "" {
		return v.verifyHeaderAuth(r)
	}
	if r.URL.Query().Get("X-Amz-Algorithm") != "" {
		return v.verifyPresignedURL(r)
	}

	return ErrMissingAuthenticationToken
}

func (v *Verifier) verifyHeaderAuth(r *http.Request) error {
	authz, err := parseAuthorizationHeader(r.Header.Get("Authorization"))
	if err != nil {
		return err
	}

	requestTime, err := parseAmzDate(r.Header.Get("X-Amz-Date"))
	if err != nil {
		return ErrInvalidRequest
	}
	if v.skewExceeded(requestTime) {
		return ErrRequestTimeTooSkewed
	}

	if authz.Scope.Service != v.service || authz.Scope.Terminal != v.terminal {
		return ErrInvalidRequest
	}

	secrets, exists, active := v.store.resolveSecrets(authz.Scope.AccessKeyID, v.now())
	if !exists {
		return ErrInvalidAccessKeyID
	}
	if !active {
		return ErrAccessDenied
	}

	canonicalRequest, err := buildCanonicalRequest(r, authz.SignedHeaders, false)
	if err != nil {
		return err
	}
	stringToSign := buildStringToSign(requestTime, authz.Scope.Raw, canonicalRequest)

	for _, secret := range secrets {
		expected := signString(secret, authz.Scope.Date, authz.Scope.Region, authz.Scope.Service, authz.Scope.Terminal, stringToSign)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(authz.Signature)) == 1 {
			return nil
		}
	}

	return ErrSignatureDoesNotMatch
}

func (v *Verifier) verifyPresignedURL(r *http.Request) error {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		return ErrInvalidRequest
	}

	params := r.URL.Query()
	if params.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
		return ErrInvalidRequest
	}

	requestTime, err := parseAmzDate(params.Get("X-Amz-Date"))
	if err != nil {
		return ErrInvalidRequest
	}
	if v.skewExceeded(requestTime) {
		return ErrRequestTimeTooSkewed
	}

	expiresSeconds, err := strconv.Atoi(params.Get("X-Amz-Expires"))
	if err != nil || expiresSeconds < 0 {
		return ErrInvalidRequest
	}
	if v.now().UTC().After(requestTime.Add(time.Duration(expiresSeconds) * time.Second)) {
		return ErrAccessDenied
	}

	scope, err := parseCredentialScope(params.Get("X-Amz-Credential"))
	if err != nil {
		return ErrInvalidRequest
	}
	if scope.Service != v.service || scope.Terminal != v.terminal {
		return ErrInvalidRequest
	}

	signedHeaders := strings.Split(params.Get("X-Amz-SignedHeaders"), ";")
	if len(signedHeaders) == 0 || signedHeaders[0] == "" {
		return ErrInvalidRequest
	}

	signature := params.Get("X-Amz-Signature")
	if signature == "" {
		return ErrInvalidRequest
	}

	secrets, exists, active := v.store.resolveSecrets(scope.AccessKeyID, v.now())
	if !exists {
		return ErrInvalidAccessKeyID
	}
	if !active {
		return ErrAccessDenied
	}

	canonicalRequest, err := buildCanonicalRequest(r, signedHeaders, true)
	if err != nil {
		return err
	}
	stringToSign := buildStringToSign(requestTime, scope.Raw, canonicalRequest)

	for _, secret := range secrets {
		expected := signString(secret, scope.Date, scope.Region, scope.Service, scope.Terminal, stringToSign)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1 {
			return nil
		}
	}

	return ErrSignatureDoesNotMatch
}

func (v *Verifier) skewExceeded(requestTime time.Time) bool {
	now := v.now().UTC()
	delta := now.Sub(requestTime.UTC())
	if delta < 0 {
		delta = -delta
	}
	return delta > v.maxSkew
}

type authHeader struct {
	Scope         credentialScope
	SignedHeaders []string
	Signature     string
}

type credentialScope struct {
	Raw         string
	AccessKeyID string
	Date        string
	Region      string
	Service     string
	Terminal    string
}

func parseAuthorizationHeader(header string) (authHeader, error) {
	if !strings.HasPrefix(header, "AWS4-HMAC-SHA256 ") {
		return authHeader{}, ErrInvalidRequest
	}

	rest := strings.TrimPrefix(header, "AWS4-HMAC-SHA256 ")
	parts := strings.Split(rest, ",")
	values := make(map[string]string, len(parts))
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			return authHeader{}, ErrInvalidRequest
		}
		values[kv[0]] = kv[1]
	}

	scope, err := parseCredentialScope(values["Credential"])
	if err != nil {
		return authHeader{}, ErrInvalidRequest
	}

	signedHeaders := strings.Split(values["SignedHeaders"], ";")
	if len(signedHeaders) == 0 || signedHeaders[0] == "" {
		return authHeader{}, ErrInvalidRequest
	}

	signature := values["Signature"]
	if signature == "" {
		return authHeader{}, ErrInvalidRequest
	}

	return authHeader{Scope: scope, SignedHeaders: signedHeaders, Signature: signature}, nil
}

func parseCredentialScope(credential string) (credentialScope, error) {
	parts := strings.Split(credential, "/")
	if len(parts) != 5 {
		return credentialScope{}, ErrInvalidRequest
	}

	if parts[0] == "" || parts[1] == "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
		return credentialScope{}, ErrInvalidRequest
	}

	return credentialScope{
		Raw:         strings.Join(parts[1:], "/"),
		AccessKeyID: parts[0],
		Date:        parts[1],
		Region:      parts[2],
		Service:     parts[3],
		Terminal:    parts[4],
	}, nil
}

func parseAmzDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, ErrInvalidRequest
	}
	return time.Parse("20060102T150405Z", value)
}

func buildCanonicalRequest(r *http.Request, signedHeaders []string, presigned bool) (string, error) {
	normalizedHeaders := append([]string(nil), signedHeaders...)
	for i := range normalizedHeaders {
		normalizedHeaders[i] = strings.ToLower(strings.TrimSpace(normalizedHeaders[i]))
	}
	sort.Strings(normalizedHeaders)

	canonicalHeaders := make([]string, 0, len(normalizedHeaders))
	for _, name := range normalizedHeaders {
		if name == "" {
			return "", ErrInvalidRequest
		}

		value, ok := canonicalHeaderValue(r, name)
		if !ok {
			return "", ErrSignatureDoesNotMatch
		}
		canonicalHeaders = append(canonicalHeaders, name+":"+value)
	}

	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}

	canonical := strings.Join([]string{
		r.Method,
		canonicalURI(r.URL),
		canonicalQuery(r.URL.Query(), presigned),
		strings.Join(canonicalHeaders, "\n") + "\n",
		strings.Join(normalizedHeaders, ";"),
		payloadHash,
	}, "\n")

	return canonical, nil
}

func canonicalHeaderValue(r *http.Request, headerName string) (string, bool) {
	if headerName == "host" {
		host := r.Host
		if host == "" && r.URL != nil {
			host = r.URL.Host
		}
		host = strings.TrimSpace(host)
		if host == "" {
			return "", false
		}
		return host, true
	}

	values := r.Header.Values(headerName)
	if len(values) == 0 {
		return "", false
	}

	for i := range values {
		values[i] = strings.Join(strings.Fields(values[i]), " ")
	}

	return strings.Join(values, ","), true
}

func canonicalURI(u *url.URL) string {
	if u == nil {
		return "/"
	}
	if u.EscapedPath() == "" {
		return "/"
	}
	return u.EscapedPath()
}

func canonicalQuery(values url.Values, presigned bool) string {
	type pair struct {
		key   string
		value string
	}

	pairs := make([]pair, 0)
	for key, vals := range values {
		if presigned && strings.EqualFold(key, "X-Amz-Signature") {
			continue
		}

		sortedVals := append([]string(nil), vals...)
		sort.Strings(sortedVals)
		for _, val := range sortedVals {
			pairs = append(pairs, pair{key: key, value: val})
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})

	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, awsEncode(p.key)+"="+awsEncode(p.value))
	}

	return strings.Join(out, "&")
}

func buildStringToSign(requestTime time.Time, scope string, canonicalRequest string) string {
	return strings.Join([]string{
		"AWS4-HMAC-SHA256",
		requestTime.UTC().Format("20060102T150405Z"),
		scope,
		hexSHA256(canonicalRequest),
	}, "\n")
}

func signString(secret string, date string, region string, service string, terminal string, stringToSign string) string {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, terminal)
	return hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}

func hexSHA256(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func awsEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}
