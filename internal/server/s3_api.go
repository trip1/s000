package server

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

const (
	maxMultipartParts    = 10000
	minMultipartPartSize = 5 * 1024 * 1024
)

type s3API struct {
	domain       string
	store        metadata.Store
	blob         *blob.Store
	region       string
	heavy        *heavyOpLimiter
	auditEnabled bool
	audit        AuditSink
	sseKey       []byte
}

func newS3API(opts Options) *s3API {
	region := opts.BucketRegion
	if region == "" {
		region = "us-east-1"
	}
	return &s3API{
		domain:       opts.Domain,
		store:        opts.Metadata,
		blob:         opts.Blob,
		region:       region,
		heavy:        newHeavyOpLimiter(opts.HeavyOpsWorkers, opts.HeavyOpsQueue, opts.Metrics.SetWorkerQueueDepth),
		auditEnabled: opts.AuditEnabled,
		audit:        opts.Audit,
		sseKey:       opts.SSEMasterKey,
	}
}

func (a *s3API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.store == nil || a.blob == nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusServiceUnavailable, Code: "ServiceUnavailable", Message: "Metadata/blob storage is not initialized.", Resource: r.URL.Path})
		return
	}

	if r.URL.Path == "/" && r.Method == http.MethodGet {
		a.handleListBuckets(w, r)
		return
	}

	bucket, key, ok := bucketAndKey(r, a.domain)
	if !ok {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: r.URL.Path})
		return
	}

	if key == "" {
		a.handleBucketRequest(w, r, bucket)
		return
	}
	a.handleObjectRequest(w, r, bucket, key)
}

func (a *s3API) handleBucketRequest(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPut && hasQueryFlag(q, "website"):
		a.handlePutBucketWebsite(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "website"):
		a.handleGetBucketWebsite(w, r, bucket)
	case r.Method == http.MethodDelete && hasQueryFlag(q, "website"):
		a.handleDeleteBucketWebsite(w, r, bucket)
	case r.Method == http.MethodPost && hasQueryFlag(q, "delete"):
		a.handleDeleteObjects(w, r, bucket)
	case r.Method == http.MethodPut && hasQueryFlag(q, "cors"):
		a.handlePutBucketCORS(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "cors"):
		a.handleGetBucketCORS(w, r, bucket)
	case r.Method == http.MethodDelete && hasQueryFlag(q, "cors"):
		a.handleDeleteBucketCORS(w, r, bucket)
	case r.Method == http.MethodPut && hasQueryFlag(q, "policy"):
		a.handlePutBucketPolicy(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "policy"):
		a.handleGetBucketPolicy(w, r, bucket)
	case r.Method == http.MethodDelete && hasQueryFlag(q, "policy"):
		a.handleDeleteBucketPolicy(w, r, bucket)
	case r.Method == http.MethodPut && hasQueryFlag(q, "publicAccessBlock"):
		a.handlePutPublicAccessBlock(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "publicAccessBlock"):
		a.handleGetPublicAccessBlock(w, r, bucket)
	case r.Method == http.MethodDelete && hasQueryFlag(q, "publicAccessBlock"):
		a.handleDeletePublicAccessBlock(w, r, bucket)
	case r.Method == http.MethodPut && hasQueryFlag(q, "lifecycle"):
		a.handlePutBucketLifecycle(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "lifecycle"):
		a.handleGetBucketLifecycle(w, r, bucket)
	case r.Method == http.MethodDelete && hasQueryFlag(q, "lifecycle"):
		a.handleDeleteBucketLifecycle(w, r, bucket)
	case r.Method == http.MethodPut && hasQueryFlag(q, "notification"):
		a.handlePutBucketNotification(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "notification"):
		a.handleGetBucketNotification(w, r, bucket)
	case r.Method == http.MethodDelete && hasQueryFlag(q, "notification"):
		a.handleDeleteBucketNotification(w, r, bucket)
	case r.Method == http.MethodPut && hasQueryFlag(q, "replication"):
		a.handlePutBucketReplication(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "replication"):
		a.handleGetBucketReplication(w, r, bucket)
	case r.Method == http.MethodDelete && hasQueryFlag(q, "replication"):
		a.handleDeleteBucketReplication(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "acl"):
		a.handleGetACL(w, r, bucket, "")
	case r.Method == http.MethodPut && hasQueryFlag(q, "acl"):
		a.handlePutACL(w, r, bucket, "")
	case r.Method == http.MethodPut && hasQueryFlag(q, "versioning"):
		a.handlePutBucketVersioning(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "versioning"):
		a.handleGetBucketVersioning(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "versions"):
		a.handleListObjectVersions(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "location"):
		a.handleGetBucketLocation(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "uploads"):
		a.handleListMultipartUploads(w, r, bucket)
	case r.Method == http.MethodGet && q.Get("list-type") == "2":
		a.handleListObjectsV2(w, r, bucket)
	case r.Method == http.MethodPut:
		a.handleCreateBucket(w, r, bucket)
	case r.Method == http.MethodDelete:
		a.handleDeleteBucket(w, r, bucket)
	default:
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusMethodNotAllowed, Code: "MethodNotAllowed", Message: "Method not allowed.", Resource: "/" + bucket})
	}
}

func (a *s3API) handlePutBucketWebsite(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}

	type websiteXML struct {
		XMLName xml.Name `xml:"WebsiteConfiguration"`
		Index   struct {
			Suffix string `xml:"Suffix"`
		} `xml:"IndexDocument"`
		Error struct {
			Key string `xml:"Key"`
		} `xml:"ErrorDocument"`
		Redirect struct {
			HostName string `xml:"HostName"`
			Protocol string `xml:"Protocol"`
		} `xml:"RedirectAllRequestsTo"`
		RoutingRules []struct {
			Condition struct {
				KeyPrefixEquals             string `xml:"KeyPrefixEquals"`
				HTTPErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals"`
			} `xml:"Condition"`
			Redirect struct {
				HostName             string `xml:"HostName"`
				Protocol             string `xml:"Protocol"`
				ReplaceKeyPrefixWith string `xml:"ReplaceKeyPrefixWith"`
				ReplaceKeyWith       string `xml:"ReplaceKeyWith"`
				HTTPRedirectCode     string `xml:"HttpRedirectCode"`
			} `xml:"Redirect"`
		} `xml:"RoutingRules>RoutingRule"`
	}
	var in websiteXML
	if err := xml.NewDecoder(r.Body).Decode(&in); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed or did not validate against our published schema.", Resource: "/" + bucket})
		return
	}
	idx := strings.TrimSpace(in.Index.Suffix)
	errDoc := strings.TrimSpace(in.Error.Key)
	redirectHost := strings.TrimSpace(in.Redirect.HostName)
	redirectProto := strings.TrimSpace(strings.ToLower(in.Redirect.Protocol))
	if idx == "" && redirectHost == "" && len(in.RoutingRules) == 0 {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "Website configuration requires IndexDocument or RedirectAllRequestsTo.", Resource: "/" + bucket})
		return
	}
	if redirectProto != "" && redirectProto != "http" && redirectProto != "https" {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "Redirect protocol must be http or https.", Resource: "/" + bucket})
		return
	}
	routingRules := make([]metadata.BucketWebsiteRoutingRule, 0, len(in.RoutingRules))
	for _, rr := range in.RoutingRules {
		rule := metadata.BucketWebsiteRoutingRule{
			Condition: metadata.BucketWebsiteRoutingCondition{
				KeyPrefixEquals:             strings.TrimSpace(rr.Condition.KeyPrefixEquals),
				HttpErrorCodeReturnedEquals: strings.TrimSpace(rr.Condition.HTTPErrorCodeReturnedEquals),
			},
			Redirect: metadata.BucketWebsiteRedirect{
				HostName:             strings.TrimSpace(rr.Redirect.HostName),
				Protocol:             strings.TrimSpace(strings.ToLower(rr.Redirect.Protocol)),
				ReplaceKeyPrefixWith: strings.TrimSpace(rr.Redirect.ReplaceKeyPrefixWith),
				ReplaceKeyWith:       strings.TrimSpace(rr.Redirect.ReplaceKeyWith),
				HTTPRedirectCode:     strings.TrimSpace(rr.Redirect.HTTPRedirectCode),
			},
		}
		if err := validateWebsiteRoutingRule(rule); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: err.Error(), Resource: "/" + bucket})
			return
		}
		routingRules = append(routingRules, rule)
	}

	if err := a.store.PutBucketWebsite(r.Context(), metadata.BucketWebsiteConfig{
		Bucket:              bucket,
		IndexDocument:       idx,
		ErrorDocument:       errDoc,
		RedirectAllHost:     redirectHost,
		RedirectAllProtocol: redirectProto,
		RoutingRules:        routingRules,
		Enabled:             true,
		PublicRead:          true,
	}); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put website configuration failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleGetBucketWebsite(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	cfg, err := a.store.GetBucketWebsite(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchWebsiteConfiguration", Message: "The specified bucket does not have a website configuration.", Resource: "/" + bucket})
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Get website configuration failed.", Resource: "/" + bucket})
		return
	}

	type outXML struct {
		XMLName xml.Name `xml:"WebsiteConfiguration"`
		Index   *struct {
			Suffix string `xml:"Suffix"`
		} `xml:"IndexDocument,omitempty"`
		Error *struct {
			Key string `xml:"Key"`
		} `xml:"ErrorDocument,omitempty"`
		Redirect *struct {
			HostName string `xml:"HostName"`
			Protocol string `xml:"Protocol,omitempty"`
		} `xml:"RedirectAllRequestsTo,omitempty"`
		RoutingRules *struct {
			Rules []struct {
				Condition *struct {
					KeyPrefixEquals             string `xml:"KeyPrefixEquals,omitempty"`
					HTTPErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals,omitempty"`
				} `xml:"Condition,omitempty"`
				Redirect struct {
					HostName             string `xml:"HostName,omitempty"`
					Protocol             string `xml:"Protocol,omitempty"`
					ReplaceKeyPrefixWith string `xml:"ReplaceKeyPrefixWith,omitempty"`
					ReplaceKeyWith       string `xml:"ReplaceKeyWith,omitempty"`
					HTTPRedirectCode     string `xml:"HttpRedirectCode,omitempty"`
				} `xml:"Redirect"`
			} `xml:"RoutingRule"`
		} `xml:"RoutingRules,omitempty"`
	}
	out := outXML{}
	if cfg.IndexDocument != "" {
		out.Index = &struct {
			Suffix string `xml:"Suffix"`
		}{Suffix: cfg.IndexDocument}
	}
	if cfg.ErrorDocument != "" {
		out.Error = &struct {
			Key string `xml:"Key"`
		}{Key: cfg.ErrorDocument}
	}
	if cfg.RedirectAllHost != "" {
		out.Redirect = &struct {
			HostName string `xml:"HostName"`
			Protocol string `xml:"Protocol,omitempty"`
		}{HostName: cfg.RedirectAllHost, Protocol: cfg.RedirectAllProtocol}
	}
	if len(cfg.RoutingRules) > 0 {
		rules := make([]struct {
			Condition *struct {
				KeyPrefixEquals             string `xml:"KeyPrefixEquals,omitempty"`
				HTTPErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals,omitempty"`
			} `xml:"Condition,omitempty"`
			Redirect struct {
				HostName             string `xml:"HostName,omitempty"`
				Protocol             string `xml:"Protocol,omitempty"`
				ReplaceKeyPrefixWith string `xml:"ReplaceKeyPrefixWith,omitempty"`
				ReplaceKeyWith       string `xml:"ReplaceKeyWith,omitempty"`
				HTTPRedirectCode     string `xml:"HttpRedirectCode,omitempty"`
			} `xml:"Redirect"`
		}, 0, len(cfg.RoutingRules))
		for _, rr := range cfg.RoutingRules {
			item := struct {
				Condition *struct {
					KeyPrefixEquals             string `xml:"KeyPrefixEquals,omitempty"`
					HTTPErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals,omitempty"`
				} `xml:"Condition,omitempty"`
				Redirect struct {
					HostName             string `xml:"HostName,omitempty"`
					Protocol             string `xml:"Protocol,omitempty"`
					ReplaceKeyPrefixWith string `xml:"ReplaceKeyPrefixWith,omitempty"`
					ReplaceKeyWith       string `xml:"ReplaceKeyWith,omitempty"`
					HTTPRedirectCode     string `xml:"HttpRedirectCode,omitempty"`
				} `xml:"Redirect"`
			}{}
			if rr.Condition.KeyPrefixEquals != "" || rr.Condition.HttpErrorCodeReturnedEquals != "" {
				item.Condition = &struct {
					KeyPrefixEquals             string `xml:"KeyPrefixEquals,omitempty"`
					HTTPErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals,omitempty"`
				}{
					KeyPrefixEquals:             rr.Condition.KeyPrefixEquals,
					HTTPErrorCodeReturnedEquals: rr.Condition.HttpErrorCodeReturnedEquals,
				}
			}
			item.Redirect.HostName = rr.Redirect.HostName
			item.Redirect.Protocol = rr.Redirect.Protocol
			item.Redirect.ReplaceKeyPrefixWith = rr.Redirect.ReplaceKeyPrefixWith
			item.Redirect.ReplaceKeyWith = rr.Redirect.ReplaceKeyWith
			item.Redirect.HTTPRedirectCode = rr.Redirect.HTTPRedirectCode
			rules = append(rules, item)
		}
		out.RoutingRules = &struct {
			Rules []struct {
				Condition *struct {
					KeyPrefixEquals             string `xml:"KeyPrefixEquals,omitempty"`
					HTTPErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals,omitempty"`
				} `xml:"Condition,omitempty"`
				Redirect struct {
					HostName             string `xml:"HostName,omitempty"`
					Protocol             string `xml:"Protocol,omitempty"`
					ReplaceKeyPrefixWith string `xml:"ReplaceKeyPrefixWith,omitempty"`
					ReplaceKeyWith       string `xml:"ReplaceKeyWith,omitempty"`
					HTTPRedirectCode     string `xml:"HttpRedirectCode,omitempty"`
				} `xml:"Redirect"`
			} `xml:"RoutingRule"`
		}{Rules: rules}
	}
	writeXML(w, http.StatusOK, out)
}

func validateWebsiteRoutingRule(rule metadata.BucketWebsiteRoutingRule) error {
	if rule.Redirect.Protocol != "" && rule.Redirect.Protocol != "http" && rule.Redirect.Protocol != "https" {
		return fmt.Errorf("routing rule redirect protocol must be http or https")
	}
	if rule.Redirect.ReplaceKeyPrefixWith != "" && rule.Redirect.ReplaceKeyWith != "" {
		return fmt.Errorf("routing rule redirect cannot set both ReplaceKeyPrefixWith and ReplaceKeyWith")
	}
	if rule.Redirect.HostName == "" && rule.Redirect.Protocol == "" && rule.Redirect.ReplaceKeyPrefixWith == "" && rule.Redirect.ReplaceKeyWith == "" && rule.Redirect.HTTPRedirectCode == "" {
		return fmt.Errorf("routing rule redirect requires at least one redirect field")
	}
	if rule.Condition.HttpErrorCodeReturnedEquals != "" {
		n, err := strconv.Atoi(rule.Condition.HttpErrorCodeReturnedEquals)
		if err != nil || n < 100 || n > 599 {
			return fmt.Errorf("routing rule condition HttpErrorCodeReturnedEquals must be a valid HTTP status code")
		}
	}
	if rule.Redirect.HTTPRedirectCode != "" {
		n, err := strconv.Atoi(rule.Redirect.HTTPRedirectCode)
		if err != nil || n < 300 || n > 399 {
			return fmt.Errorf("routing rule HttpRedirectCode must be a valid 3xx status code")
		}
	}
	return nil
}

func (a *s3API) handleDeleteBucketWebsite(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	err := a.store.DeleteBucketWebsite(r.Context(), bucket)
	if err != nil && !errors.Is(err, metadata.ErrNotFound) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete website configuration failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) handlePutBucketCORS(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	var in struct {
		Rules []struct {
			AllowedOrigins []string `xml:"AllowedOrigin"`
			AllowedMethods []string `xml:"AllowedMethod"`
			AllowedHeaders []string `xml:"AllowedHeader"`
			ExposeHeaders  []string `xml:"ExposeHeader"`
			MaxAgeSeconds  int      `xml:"MaxAgeSeconds"`
		} `xml:"CORSRule"`
	}
	if err := xml.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Rules) == 0 {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
		return
	}
	rule := in.Rules[0]
	if len(rule.AllowedOrigins) == 0 || len(rule.AllowedMethods) == 0 {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "CORSRule requires AllowedOrigin and AllowedMethod.", Resource: "/" + bucket})
		return
	}
	err := a.store.PutBucketCORS(r.Context(), metadata.BucketCORSConfig{Bucket: bucket, AllowedOrigins: strings.Join(rule.AllowedOrigins, ","), AllowedMethods: strings.Join(rule.AllowedMethods, ","), AllowedHeaders: strings.Join(rule.AllowedHeaders, ","), ExposeHeaders: strings.Join(rule.ExposeHeaders, ","), MaxAgeSeconds: rule.MaxAgeSeconds, Enabled: true})
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put bucket CORS failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleGetBucketCORS(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := a.store.GetBucketCORS(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchCORSConfiguration", Message: "The CORS configuration does not exist.", Resource: "/" + bucket})
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Get bucket CORS failed.", Resource: "/" + bucket})
		return
	}
	type corsRule struct {
		AllowedOrigins []string `xml:"AllowedOrigin"`
		AllowedMethods []string `xml:"AllowedMethod"`
		AllowedHeaders []string `xml:"AllowedHeader,omitempty"`
		ExposeHeaders  []string `xml:"ExposeHeader,omitempty"`
		MaxAgeSeconds  int      `xml:"MaxAgeSeconds,omitempty"`
	}
	type corsOut struct {
		XMLName xml.Name   `xml:"CORSConfiguration"`
		Rules   []corsRule `xml:"CORSRule"`
	}
	writeXML(w, http.StatusOK, corsOut{Rules: []corsRule{{AllowedOrigins: splitCSV(cfg.AllowedOrigins), AllowedMethods: splitCSV(cfg.AllowedMethods), AllowedHeaders: splitCSV(cfg.AllowedHeaders), ExposeHeaders: splitCSV(cfg.ExposeHeaders), MaxAgeSeconds: cfg.MaxAgeSeconds}}})
}

func (a *s3API) handleDeleteBucketCORS(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := a.store.DeleteBucketCORS(r.Context(), bucket); err != nil && !errors.Is(err, metadata.ErrNotFound) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete bucket CORS failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) handlePutBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil || !json.Valid(body) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedPolicy", Message: "The policy document is not valid JSON.", Resource: "/" + bucket})
		return
	}
	if err := a.store.PutBucketPolicy(r.Context(), metadata.BucketPolicy{Bucket: bucket, Document: string(body), Enabled: true}); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put bucket policy failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) handleGetBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := a.store.GetBucketPolicy(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucketPolicy", Message: "The bucket policy does not exist.", Resource: "/" + bucket})
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Get bucket policy failed.", Resource: "/" + bucket})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(cfg.Document))
}

func (a *s3API) handleDeleteBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := a.store.DeleteBucketPolicy(r.Context(), bucket); err != nil && !errors.Is(err, metadata.ErrNotFound) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete bucket policy failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) handlePutPublicAccessBlock(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	var in struct {
		BlockPublicACLs       bool `xml:"BlockPublicAcls"`
		IgnorePublicACLs      bool `xml:"IgnorePublicAcls"`
		BlockPublicPolicy     bool `xml:"BlockPublicPolicy"`
		RestrictPublicBuckets bool `xml:"RestrictPublicBuckets"`
	}
	if err := xml.NewDecoder(r.Body).Decode(&in); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
		return
	}
	if err := a.store.PutBucketPublicAccessBlock(r.Context(), metadata.BucketPublicAccessBlock{Bucket: bucket, BlockPublicACLs: in.BlockPublicACLs, IgnorePublicACLs: in.IgnorePublicACLs, BlockPublicPolicy: in.BlockPublicPolicy, RestrictPublicBuckets: in.RestrictPublicBuckets}); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put public access block failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleGetPublicAccessBlock(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := a.store.GetBucketPublicAccessBlock(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchPublicAccessBlockConfiguration", Message: "The public access block configuration was not found.", Resource: "/" + bucket})
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Get public access block failed.", Resource: "/" + bucket})
		return
	}
	type out struct {
		XMLName               xml.Name `xml:"PublicAccessBlockConfiguration"`
		BlockPublicACLs       bool     `xml:"BlockPublicAcls"`
		IgnorePublicACLs      bool     `xml:"IgnorePublicAcls"`
		BlockPublicPolicy     bool     `xml:"BlockPublicPolicy"`
		RestrictPublicBuckets bool     `xml:"RestrictPublicBuckets"`
	}
	writeXML(w, http.StatusOK, out{BlockPublicACLs: cfg.BlockPublicACLs, IgnorePublicACLs: cfg.IgnorePublicACLs, BlockPublicPolicy: cfg.BlockPublicPolicy, RestrictPublicBuckets: cfg.RestrictPublicBuckets})
}

func (a *s3API) handleDeletePublicAccessBlock(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := a.store.DeleteBucketPublicAccessBlock(r.Context(), bucket); err != nil && !errors.Is(err, metadata.ErrNotFound) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete public access block failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) handlePutBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil || !isWellFormedXML(body) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
		return
	}
	if err := a.store.PutBucketLifecycle(r.Context(), metadata.BucketLifecycle{Bucket: bucket, Document: string(body), Enabled: true}); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put bucket lifecycle failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleGetBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := a.store.GetBucketLifecycle(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchLifecycleConfiguration", Message: "The lifecycle configuration does not exist.", Resource: "/" + bucket})
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Get bucket lifecycle failed.", Resource: "/" + bucket})
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(cfg.Document))
}

func (a *s3API) handleDeleteBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := a.store.DeleteBucketLifecycle(r.Context(), bucket); err != nil && !errors.Is(err, metadata.ErrNotFound) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete bucket lifecycle failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) handleGetACL(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if key == "" {
		if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
			return
		}
	} else {
		obj, err := a.store.GetObjectVersion(r.Context(), bucket, key, r.URL.Query().Get("versionId"))
		if err != nil || obj.DeleteMarker {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "The specified key does not exist.", Resource: "/" + bucket + "/" + key})
			return
		}
	}
	writeACLXML(w)
}

func (a *s3API) handlePutACL(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if key == "" {
		if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
			return
		}
	} else {
		obj, err := a.store.GetObjectVersion(r.Context(), bucket, key, r.URL.Query().Get("versionId"))
		if err != nil || obj.DeleteMarker {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "The specified key does not exist.", Resource: "/" + bucket + "/" + key})
			return
		}
	}
	if acl := strings.TrimSpace(r.Header.Get("x-amz-acl")); acl != "" && !isSupportedCannedACL(acl) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotImplemented, Code: "NotImplemented", Message: "The requested ACL is not supported.", Resource: r.URL.Path})
		return
	}
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleObjectRequest(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}

	if r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "" {
		a.handleCopyObject(w, r, bucket, key)
		return
	}
	q := r.URL.Query()
	if hasQueryFlag(q, "acl") {
		if r.Method == http.MethodGet {
			a.handleGetACL(w, r, bucket, key)
			return
		}
		if r.Method == http.MethodPut {
			a.handlePutACL(w, r, bucket, key)
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusMethodNotAllowed, Code: "MethodNotAllowed", Message: "Method not allowed.", Resource: "/" + bucket + "/" + key})
		return
	}
	if hasQueryFlag(q, "tagging") {
		a.handleObjectTagging(w, r, bucket, key)
		return
	}
	if hasQueryFlag(q, "retention") {
		a.handleObjectRetention(w, r, bucket, key)
		return
	}
	if hasQueryFlag(q, "legal-hold") {
		a.handleObjectLegalHold(w, r, bucket, key)
		return
	}
	if hasQueryFlag(q, "uploadId") {
		a.handleMultipartWithUploadID(w, r, bucket, key)
		return
	}
	if r.Method == http.MethodPost && hasQueryFlag(q, "uploads") {
		a.handleCreateMultipartUpload(w, r, bucket, key)
		return
	}

	switch r.Method {
	case http.MethodPut:
		a.handlePutObject(w, r, bucket, key)
	case http.MethodGet:
		a.handleGetObject(w, r, bucket, key, false)
	case http.MethodHead:
		a.handleGetObject(w, r, bucket, key, true)
	case http.MethodDelete:
		a.handleDeleteObject(w, r, bucket, key)
	default:
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusMethodNotAllowed, Code: "MethodNotAllowed", Message: "Method not allowed.", Resource: "/" + bucket + "/" + key})
	}
}

func (a *s3API) handleMultipartWithUploadID(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "uploadId is required.", Resource: "/" + bucket + "/" + key})
		return
	}

	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPut && q.Get("partNumber") != "":
		a.handleUploadPart(w, r, bucket, key, uploadID)
	case r.Method == http.MethodGet:
		a.handleListParts(w, r, bucket, key, uploadID)
	case r.Method == http.MethodPost:
		a.handleCompleteMultipartUpload(w, r, bucket, key, uploadID)
	case r.Method == http.MethodDelete:
		a.handleAbortMultipartUpload(w, r, bucket, key, uploadID)
	default:
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusMethodNotAllowed, Code: "MethodNotAllowed", Message: "Method not allowed.", Resource: "/" + bucket + "/" + key})
	}
}

func (a *s3API) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	err := a.store.CreateBucket(r.Context(), metadata.Bucket{Name: bucket, CreatedAt: time.Now().UTC(), Region: a.region, VersioningStatus: "Suspended"})
	if err != nil {
		if err == metadata.ErrConflict {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusConflict, Code: "BucketAlreadyOwnedByYou", Message: "Your previous request to create the named bucket succeeded and you already own it.", Resource: "/" + bucket})
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Create bucket failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := a.store.ListBuckets(r.Context())
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "List buckets failed.", Resource: "/"})
		return
	}
	type bucketEntry struct {
		Name         string `xml:"Name"`
		CreationDate string `xml:"CreationDate"`
	}
	type result struct {
		XMLName xml.Name `xml:"ListAllMyBucketsResult"`
		Buckets struct {
			Items []bucketEntry `xml:"Bucket"`
		} `xml:"Buckets"`
	}
	out := result{}
	for _, b := range buckets {
		out.Buckets.Items = append(out.Buckets.Items, bucketEntry{Name: b.Name, CreationDate: b.CreatedAt.Format(time.RFC3339)})
	}
	writeXML(w, http.StatusOK, out)
}

func (a *s3API) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	objects, err := a.store.ListObjects(r.Context(), bucket)
	if err != nil {
		a.emitAudit(r, "bucket.delete", "deny", bucket, "", "NoSuchBucket")
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	if len(objects) > 0 {
		a.emitAudit(r, "bucket.delete", "deny", bucket, "", "BucketNotEmpty")
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusConflict, Code: "BucketNotEmpty", Message: "The bucket you tried to delete is not empty.", Resource: "/" + bucket})
		return
	}
	if err := a.store.DeleteBucket(r.Context(), bucket); err != nil {
		a.emitAudit(r, "bucket.delete", "error", bucket, "", "InternalError")
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete bucket failed.", Resource: "/" + bucket})
		return
	}
	a.emitAudit(r, "bucket.delete", "allow", bucket, "", "")
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) handleGetBucketLocation(w http.ResponseWriter, r *http.Request, bucket string) {
	b, err := a.store.GetBucket(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	type location struct {
		XMLName            xml.Name `xml:"LocationConstraint"`
		LocationConstraint string   `xml:",chardata"`
	}
	writeXML(w, http.StatusOK, location{LocationConstraint: b.Region})
}

func (a *s3API) handlePutBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "Could not read request body.", Resource: "/" + bucket})
		return
	}
	var req struct {
		Status string `xml:"Status"`
	}
	if err := xml.Unmarshal(body, &req); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
		return
	}
	if req.Status != "Enabled" && req.Status != "Suspended" {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "Unsupported versioning status.", Resource: "/" + bucket})
		return
	}
	if err := a.store.UpdateBucketVersioning(r.Context(), bucket, req.Status); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleGetBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	b, err := a.store.GetBucket(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	type versioning struct {
		XMLName xml.Name `xml:"VersioningConfiguration"`
		Status  string   `xml:"Status,omitempty"`
	}
	writeXML(w, http.StatusOK, versioning{Status: b.VersioningStatus})
}

func (a *s3API) handleListObjectsV2(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	maxKeys := 1000
	if v := q.Get("max-keys"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 && parsed <= 1000 {
			maxKeys = parsed
		}
	}
	continuation := q.Get("continuation-token")

	startAfter, err := decodeListContinuationToken(continuation, prefix, delimiter)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidArgument", Message: "Invalid continuation-token.", Resource: "/" + bucket})
		return
	}

	type content struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
		ETag string `xml:"ETag"`
	}
	type commonPrefix struct {
		Prefix string `xml:"Prefix"`
	}
	type listResult struct {
		XMLName               xml.Name       `xml:"ListBucketResult"`
		Name                  string         `xml:"Name"`
		Prefix                string         `xml:"Prefix,omitempty"`
		Delimiter             string         `xml:"Delimiter,omitempty"`
		MaxKeys               int            `xml:"MaxKeys"`
		KeyCount              int            `xml:"KeyCount"`
		IsTruncated           bool           `xml:"IsTruncated"`
		ContinuationToken     string         `xml:"ContinuationToken,omitempty"`
		NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
		Contents              []content      `xml:"Contents"`
		CommonPrefixes        []commonPrefix `xml:"CommonPrefixes,omitempty"`
	}
	out := listResult{Name: bucket, Prefix: prefix, Delimiter: delimiter, MaxKeys: maxKeys, ContinuationToken: continuation}
	if pager, ok := a.store.(metadata.ListObjectsV2Store); ok {
		page, err := pager.ListObjectsV2(r.Context(), bucket, metadata.ListObjectsV2Options{Prefix: prefix, Delimiter: delimiter, StartAfter: startAfter, MaxKeys: maxKeys})
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
			return
		}
		for _, entry := range page.Entries {
			if entry.Object != nil {
				out.Contents = append(out.Contents, content{Key: entry.Value, Size: entry.Object.Size, ETag: quotedETag(entry.Object.ETag)})
			} else {
				out.CommonPrefixes = append(out.CommonPrefixes, commonPrefix{Prefix: entry.Value})
			}
			out.KeyCount++
		}
		if page.IsTruncated {
			token, encErr := encodeListContinuationToken(prefix, delimiter, page.NextAfter)
			if encErr != nil {
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "List objects failed.", Resource: "/" + bucket})
				return
			}
			out.IsTruncated = true
			out.NextContinuationToken = token
		}
		writeXML(w, http.StatusOK, out)
		return
	}

	objects, err := a.store.ListObjects(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	entries := listV2EntriesFromObjects(objects, prefix, delimiter)
	startIdx := 0
	if startAfter != "" {
		startIdx = len(entries)
		for i, entry := range entries {
			if entry.Value > startAfter {
				startIdx = i
				break
			}
		}
	}

	for i := startIdx; i < len(entries) && out.KeyCount < maxKeys; i++ {
		entry := entries[i]
		if entry.Object != nil {
			out.Contents = append(out.Contents, content{Key: entry.Value, Size: entry.Object.Size, ETag: quotedETag(entry.Object.ETag)})
		} else {
			out.CommonPrefixes = append(out.CommonPrefixes, commonPrefix{Prefix: entry.Value})
		}
		out.KeyCount++
		if out.KeyCount == maxKeys && i+1 < len(entries) {
			token, encErr := encodeListContinuationToken(prefix, delimiter, entry.Value)
			if encErr != nil {
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "List objects failed.", Resource: "/" + bucket})
				return
			}
			out.IsTruncated = true
			out.NextContinuationToken = token
		}
	}

	writeXML(w, http.StatusOK, out)
}

func (a *s3API) handleListObjectVersions(w http.ResponseWriter, r *http.Request, bucket string) {
	versions, err := a.store.ListObjectVersions(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	q := r.URL.Query()
	prefix := q.Get("prefix")
	keyMarker := q.Get("key-marker")
	versionIDMarker := q.Get("version-id-marker")
	maxKeys := 1000
	if v := q.Get("max-keys"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 && parsed <= 1000 {
			maxKeys = parsed
		}
	}
	type versionEntry struct {
		Key          string `xml:"Key"`
		VersionID    string `xml:"VersionId"`
		IsLatest     bool   `xml:"IsLatest"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag,omitempty"`
		Size         int64  `xml:"Size,omitempty"`
	}
	type result struct {
		XMLName             xml.Name       `xml:"ListVersionsResult"`
		Name                string         `xml:"Name"`
		Prefix              string         `xml:"Prefix,omitempty"`
		KeyMarker           string         `xml:"KeyMarker,omitempty"`
		VersionIDMarker     string         `xml:"VersionIdMarker,omitempty"`
		MaxKeys             int            `xml:"MaxKeys"`
		IsTruncated         bool           `xml:"IsTruncated"`
		NextKeyMarker       string         `xml:"NextKeyMarker,omitempty"`
		NextVersionIDMarker string         `xml:"NextVersionIdMarker,omitempty"`
		Versions            []versionEntry `xml:"Version,omitempty"`
		DeleteMarkers       []versionEntry `xml:"DeleteMarker,omitempty"`
	}
	out := result{Name: bucket, Prefix: prefix, KeyMarker: keyMarker, VersionIDMarker: versionIDMarker, MaxKeys: maxKeys}
	latestByKey := map[string]string{}
	for _, version := range versions {
		if _, ok := latestByKey[version.Key]; !ok {
			latestByKey[version.Key] = version.VersionID
		}
	}
	entries := make([]metadata.ObjectVersion, 0, len(versions))
	for _, version := range versions {
		if prefix != "" && !strings.HasPrefix(version.Key, prefix) {
			continue
		}
		entries = append(entries, version)
	}
	start := 0
	if keyMarker != "" {
		start = len(entries)
		for i, version := range entries {
			if version.Key > keyMarker || (version.Key == keyMarker && versionIDMarker != "" && version.VersionID == versionIDMarker) {
				if version.Key == keyMarker && version.VersionID == versionIDMarker {
					start = i + 1
				} else {
					start = i
				}
				break
			}
		}
	}
	if maxKeys == 0 {
		out.IsTruncated = start < len(entries)
		if out.IsTruncated {
			out.NextKeyMarker = keyMarker
			out.NextVersionIDMarker = versionIDMarker
		}
		writeXML(w, http.StatusOK, out)
		return
	}
	for i := start; i < len(entries) && i < start+maxKeys; i++ {
		version := entries[i]
		entry := versionEntry{Key: version.Key, VersionID: version.VersionID, IsLatest: latestByKey[version.Key] == version.VersionID, LastModified: version.CreatedAt.Format(time.RFC3339), ETag: quotedETag(version.ETag), Size: version.Size}
		if version.DeleteMarker {
			entry.ETag = ""
			entry.Size = 0
			out.DeleteMarkers = append(out.DeleteMarkers, entry)
		} else {
			out.Versions = append(out.Versions, entry)
		}
		if i == start+maxKeys-1 && i+1 < len(entries) {
			out.IsTruncated = true
			out.NextKeyMarker = version.Key
			out.NextVersionIDMarker = version.VersionID
			break
		}
	}
	writeXML(w, http.StatusOK, out)
}

type listV2Entry struct {
	Value  string
	Object *metadata.ObjectVersion
}

func listV2EntriesFromObjects(objects []metadata.ObjectVersion, prefix string, delimiter string) []listV2Entry {
	entries := make([]listV2Entry, 0, len(objects))
	commonPrefixes := make(map[string]struct{})
	for i := range objects {
		obj := objects[i]
		if prefix != "" && !strings.HasPrefix(obj.Key, prefix) {
			continue
		}
		if delimiter != "" {
			tail := strings.TrimPrefix(obj.Key, prefix)
			if idx := strings.Index(tail, delimiter); idx >= 0 {
				cp := prefix + tail[:idx+len(delimiter)]
				if _, exists := commonPrefixes[cp]; exists {
					continue
				}
				commonPrefixes[cp] = struct{}{}
				entries = append(entries, listV2Entry{Value: cp})
				continue
			}
		}
		copyObj := obj
		entries = append(entries, listV2Entry{Value: obj.Key, Object: &copyObj})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Value < entries[j].Value
	})
	return entries
}

type listV2ContinuationToken struct {
	Prefix    string `json:"p"`
	Delimiter string `json:"d"`
	After     string `json:"a"`
}

func encodeListContinuationToken(prefix, delimiter, after string) (string, error) {
	payload, err := json.Marshal(listV2ContinuationToken{Prefix: prefix, Delimiter: delimiter, After: after})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeListContinuationToken(token, prefix, delimiter string) (string, error) {
	if token == "" {
		return "", nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}
	var decoded listV2ContinuationToken
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", err
	}
	if decoded.Prefix != prefix || decoded.Delimiter != delimiter || decoded.After == "" {
		return "", fmt.Errorf("token mismatch")
	}
	return decoded.After, nil
}

func (a *s3API) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !a.runHeavy(w, r, bucket, key, func() {
		b, err := a.store.GetBucket(r.Context(), bucket)
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
			return
		}

		versionID := "null"
		if b.VersioningStatus == "Enabled" {
			versionID = newVersionID()
		}
		if versionID == "null" {
			if existing, err := a.store.GetObjectVersion(r.Context(), bucket, key, "null"); err == nil && objectLocked(existing, time.Now().UTC()) {
				writeObjectLockedError(w, r)
				return
			}
		}
		encrypted, ok := a.validateSSEHeaders(w, r, bucket, key)
		if !ok {
			return
		}
		ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID}
		blobMeta, err := a.writeMaybeEncryptedObject(r, ref, requestObjectBody(r), encrypted)
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Object write failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		if md5Header := r.Header.Get("Content-MD5"); md5Header != "" {
			expected, err := base64.StdEncoding.DecodeString(md5Header)
			if err != nil {
				_ = a.blob.DeleteObject(r.Context(), ref, true)
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidDigest", Message: "The Content-MD5 you specified is invalid.", Resource: "/" + bucket + "/" + key})
				return
			}
			actual, _ := hex.DecodeString(blobMeta.MD5Hex)
			if !equalBytes(expected, actual) {
				_ = a.blob.DeleteObject(r.Context(), ref, true)
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "BadDigest", Message: "The Content-MD5 you specified did not match what we received.", Resource: "/" + bucket + "/" + key})
				return
			}
		}
		if !validateChecksumHeaders(w, r, blobMeta) {
			_ = a.blob.DeleteObject(r.Context(), ref, true)
			return
		}

		objectMeta := metadata.ObjectVersion{
			Bucket:         bucket,
			Key:            key,
			VersionID:      versionID,
			Size:           blobMeta.Size,
			ETag:           blobMeta.MD5Hex,
			ChecksumSHA256: blobMeta.SHA256B64,
			ChecksumSHA1:   blobMeta.SHA1B64,
			ChecksumCRC32:  blobMeta.CRC32B64,
			ChecksumCRC32C: blobMeta.CRC32CB64,
			StoragePath:    blobMeta.Path,
			Metadata:       markSSEMetadata(collectUserMetadata(r.Header), encrypted),
			CreatedAt:      blobMeta.CreatedAt,
		}
		if err := a.store.PutObjectVersion(r.Context(), objectMeta); err != nil {
			_ = a.blob.DeleteObject(r.Context(), ref, true)
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Metadata write failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		w.Header().Set("ETag", quotedETag(blobMeta.MD5Hex))
		setSSEHeader(w.Header(), objectMeta)
		setObjectChecksumHeaders(w.Header(), metadata.ObjectVersion{ChecksumSHA256: blobMeta.SHA256B64, ChecksumSHA1: blobMeta.SHA1B64, ChecksumCRC32: blobMeta.CRC32B64, ChecksumCRC32C: blobMeta.CRC32CB64})
		w.WriteHeader(http.StatusOK)
		a.emitObjectNotification(r, "ObjectCreated:Put", objectMeta)
		a.emitReplication(r, objectMeta)
	}) {
		return
	}
}

func (a *s3API) handleGetObject(w http.ResponseWriter, r *http.Request, bucket, key string, headOnly bool) {
	versionID := r.URL.Query().Get("versionId")
	obj, err := a.store.GetObjectVersion(r.Context(), bucket, key, versionID)
	if err != nil || obj.DeleteMarker {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "The specified key does not exist.", Resource: "/" + bucket + "/" + key})
		return
	}
	if !objectConditionsPass(w, r, obj, "") {
		return
	}

	w.Header().Set("ETag", quotedETag(obj.ETag))
	setObjectChecksumHeaders(w.Header(), obj)
	setSSEHeader(w.Header(), obj)
	retentionHeaders(obj, w.Header())
	w.Header().Set("Last-Modified", obj.CreatedAt.UTC().Format(http.TimeFormat))
	for k, v := range obj.Metadata {
		if strings.HasPrefix(k, "__s000_") {
			continue
		}
		w.Header().Set("x-amz-meta-"+k, v)
	}

	if headOnly {
		w.WriteHeader(http.StatusOK)
		return
	}

	meta := blob.ObjectMeta{Ref: blob.ObjectRef{Bucket: obj.Bucket, Key: obj.Key, VersionID: obj.VersionID}, Path: obj.StoragePath, Size: obj.Size, SHA256: obj.ChecksumSHA256, MD5Hex: obj.ETag, CreatedAt: obj.CreatedAt}
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		br, parseErr := parseRange(rangeHeader, obj.Size)
		if parseErr != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusRequestedRangeNotSatisfiable, Code: "InvalidRange", Message: "The requested range is not satisfiable.", Resource: "/" + bucket + "/" + key})
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		_, err = a.readMaybeEncryptedObject(r, meta, obj, br, w)
	} else {
		w.WriteHeader(http.StatusOK)
		_, err = a.readMaybeEncryptedObject(r, meta, obj, nil, w)
	}
	if err != nil {
		return
	}
}

func (a *s3API) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	b, err := a.store.GetBucket(r.Context(), bucket)
	if err != nil {
		a.emitAudit(r, "object.delete", "deny", bucket, key, "NoSuchBucket")
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	if versionID := r.URL.Query().Get("versionId"); versionID != "" {
		obj, getErr := a.store.GetObjectVersion(r.Context(), bucket, key, versionID)
		if getErr == nil && objectLocked(obj, time.Now().UTC()) {
			a.emitAudit(r, "object.delete", "deny", bucket, key, "ObjectLocked")
			writeObjectLockedError(w, r)
			return
		}
		removed, err := a.store.DeleteObjectVersion(r.Context(), bucket, key, versionID)
		if err != nil && !errors.Is(err, metadata.ErrNotFound) {
			a.emitAudit(r, "object.delete", "error", bucket, key, "InternalError")
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete object version failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		if err == nil && !removed.DeleteMarker {
			_ = a.blob.DeleteObject(r.Context(), blob.ObjectRef{Bucket: removed.Bucket, Key: removed.Key, VersionID: removed.VersionID}, true)
		}
		a.emitAudit(r, "object.delete", "allow", bucket, key, "")
		w.WriteHeader(http.StatusNoContent)
		if err == nil {
			a.emitObjectNotification(r, "ObjectRemoved:Delete", removed)
		}
		return
	}
	if b.VersioningStatus == "Enabled" {
		marker := metadata.ObjectVersion{Bucket: bucket, Key: key, VersionID: newVersionID(), DeleteMarker: true, CreatedAt: time.Now().UTC()}
		_ = a.store.DeleteObject(r.Context(), bucket, key, marker.VersionID, marker.CreatedAt)
		a.emitAudit(r, "object.delete", "allow", bucket, key, "")
		w.WriteHeader(http.StatusNoContent)
		a.emitObjectNotification(r, "ObjectRemoved:DeleteMarkerCreated", marker)
		return
	}
	if existing, err := a.store.GetObjectVersion(r.Context(), bucket, key, ""); err == nil && objectLocked(existing, time.Now().UTC()) {
		a.emitAudit(r, "object.delete", "deny", bucket, key, "ObjectLocked")
		writeObjectLockedError(w, r)
		return
	}

	removed, err := a.store.DeleteAllObjectVersions(r.Context(), bucket, key)
	if err != nil && err != metadata.ErrNotFound {
		a.emitAudit(r, "object.delete", "error", bucket, key, "InternalError")
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete object failed.", Resource: "/" + bucket + "/" + key})
		return
	}
	for _, obj := range removed {
		_ = a.blob.DeleteObject(r.Context(), blob.ObjectRef{Bucket: obj.Bucket, Key: obj.Key, VersionID: obj.VersionID}, true)
		a.emitObjectNotification(r, "ObjectRemoved:Delete", obj)
	}
	a.emitAudit(r, "object.delete", "allow", bucket, key, "")
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) handleObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")
	obj, err := a.store.GetObjectVersion(r.Context(), bucket, key, versionID)
	if err != nil || obj.DeleteMarker {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "The specified key does not exist.", Resource: "/" + bucket + "/" + key})
		return
	}
	if versionID == "" {
		versionID = obj.VersionID
	}
	switch r.Method {
	case http.MethodPut:
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil || !isWellFormedXML(body) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket + "/" + key})
			return
		}
		if err := a.store.PutObjectTagging(r.Context(), metadata.ObjectTagging{Bucket: bucket, Key: key, VersionID: versionID, Document: string(body)}); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put object tagging failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		cfg, err := a.store.GetObjectTagging(r.Context(), bucket, key, versionID)
		if err != nil {
			if errors.Is(err, metadata.ErrNotFound) {
				writeXML(w, http.StatusOK, struct {
					XMLName xml.Name `xml:"Tagging"`
					TagSet  struct{} `xml:"TagSet"`
				}{})
				return
			}
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Get object tagging failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(cfg.Document))
	case http.MethodDelete:
		if err := a.store.DeleteObjectTagging(r.Context(), bucket, key, versionID); err != nil && !errors.Is(err, metadata.ErrNotFound) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete object tagging failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusMethodNotAllowed, Code: "MethodNotAllowed", Message: "Method not allowed.", Resource: "/" + bucket + "/" + key})
	}
}

func (a *s3API) handleObjectRetention(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")
	obj, err := a.store.GetObjectVersion(r.Context(), bucket, key, versionID)
	if err != nil || obj.DeleteMarker {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "The specified key does not exist.", Resource: "/" + bucket + "/" + key})
		return
	}
	if versionID == "" {
		versionID = obj.VersionID
	}
	switch r.Method {
	case http.MethodPut:
		var req objectRetentionXML
		if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket + "/" + key})
			return
		}
		mode := strings.ToUpper(strings.TrimSpace(req.Mode))
		if mode != "GOVERNANCE" && mode != "COMPLIANCE" {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "Retention mode must be GOVERNANCE or COMPLIANCE.", Resource: "/" + bucket + "/" + key})
			return
		}
		until, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(req.RetainUntilDate))
		if parseErr != nil || !until.After(time.Now().UTC()) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "RetainUntilDate must be a future RFC3339 timestamp.", Resource: "/" + bucket + "/" + key})
			return
		}
		if err := a.store.UpdateObjectMetadata(r.Context(), bucket, key, versionID, putRetentionMetadata(obj, mode, until)); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put object retention failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		writeXML(w, http.StatusOK, objectRetentionXML{Mode: obj.Metadata[lockRetentionModeKey], RetainUntilDate: obj.Metadata[lockRetentionRetainUntilKey]})
	default:
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusMethodNotAllowed, Code: "MethodNotAllowed", Message: "Method not allowed.", Resource: "/" + bucket + "/" + key})
	}
}

func (a *s3API) handleObjectLegalHold(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")
	obj, err := a.store.GetObjectVersion(r.Context(), bucket, key, versionID)
	if err != nil || obj.DeleteMarker {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "The specified key does not exist.", Resource: "/" + bucket + "/" + key})
		return
	}
	if versionID == "" {
		versionID = obj.VersionID
	}
	switch r.Method {
	case http.MethodPut:
		var req objectLegalHoldXML
		if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket + "/" + key})
			return
		}
		status := strings.ToUpper(strings.TrimSpace(req.Status))
		if status != "ON" && status != "OFF" {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "Legal hold status must be ON or OFF.", Resource: "/" + bucket + "/" + key})
			return
		}
		if err := a.store.UpdateObjectMetadata(r.Context(), bucket, key, versionID, putLegalHoldMetadata(obj, status)); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put object legal hold failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		writeXML(w, http.StatusOK, objectLegalHoldXML{Status: obj.Metadata[lockLegalHoldKey]})
	default:
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusMethodNotAllowed, Code: "MethodNotAllowed", Message: "Method not allowed.", Resource: "/" + bucket + "/" + key})
	}
}

func (a *s3API) handleDeleteObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	b, err := a.store.GetBucket(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	var in struct {
		Quiet   bool `xml:"Quiet"`
		Objects []struct {
			Key       string `xml:"Key"`
			VersionID string `xml:"VersionId"`
		} `xml:"Object"`
	}
	if err := xml.NewDecoder(r.Body).Decode(&in); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
		return
	}
	type deleted struct {
		Key       string `xml:"Key"`
		VersionID string `xml:"VersionId,omitempty"`
	}
	type deleteError struct {
		Key     string `xml:"Key"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	type result struct {
		XMLName xml.Name      `xml:"DeleteResult"`
		Deleted []deleted     `xml:"Deleted,omitempty"`
		Errors  []deleteError `xml:"Error,omitempty"`
	}
	out := result{}
	for _, obj := range in.Objects {
		key := strings.TrimSpace(obj.Key)
		if key == "" {
			out.Errors = append(out.Errors, deleteError{Code: "InvalidRequest", Message: "Object key is required."})
			continue
		}
		if obj.VersionID != "" {
			locked, lockErr := a.store.GetObjectVersion(r.Context(), bucket, key, obj.VersionID)
			if lockErr == nil && objectLocked(locked, time.Now().UTC()) {
				out.Errors = append(out.Errors, deleteError{Key: key, Code: "AccessDenied", Message: "Object is protected by object lock retention or legal hold."})
				continue
			}
		} else if b.VersioningStatus != "Enabled" {
			locked, lockErr := a.store.GetObjectVersion(r.Context(), bucket, key, "")
			if lockErr == nil && objectLocked(locked, time.Now().UTC()) {
				out.Errors = append(out.Errors, deleteError{Key: key, Code: "AccessDenied", Message: "Object is protected by object lock retention or legal hold."})
				continue
			}
		}
		if b.VersioningStatus == "Enabled" && obj.VersionID == "" {
			err = a.store.DeleteObject(r.Context(), bucket, key, newVersionID(), time.Now().UTC())
		} else {
			removed, delErr := a.store.DeleteAllObjectVersions(r.Context(), bucket, key)
			err = delErr
			for _, version := range removed {
				_ = a.blob.DeleteObject(r.Context(), blob.ObjectRef{Bucket: version.Bucket, Key: version.Key, VersionID: version.VersionID}, true)
				a.emitObjectNotification(r, "ObjectRemoved:Delete", version)
			}
		}
		if err != nil && !errors.Is(err, metadata.ErrNotFound) {
			out.Errors = append(out.Errors, deleteError{Key: key, Code: "InternalError", Message: "Delete object failed."})
			continue
		}
		if !in.Quiet {
			out.Deleted = append(out.Deleted, deleted{Key: key, VersionID: obj.VersionID})
		}
	}
	writeXML(w, http.StatusOK, out)
}

func (a *s3API) handleCopyObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !a.runHeavy(w, r, bucket, key, func() {
		src := strings.TrimPrefix(r.Header.Get("X-Amz-Copy-Source"), "/")
		parts := strings.SplitN(src, "/", 2)
		if len(parts) != 2 {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "Invalid copy source.", Resource: "/" + bucket + "/" + key})
			return
		}
		srcBucket, srcKey := parts[0], parts[1]
		srcObj, err := a.store.GetLatestObjectVersion(r.Context(), srcBucket, srcKey)
		if err != nil || srcObj.DeleteMarker {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "The specified key does not exist.", Resource: "/" + srcBucket + "/" + srcKey})
			return
		}
		if !objectConditionsPass(w, r, srcObj, "x-amz-copy-source-") {
			return
		}
		encrypted, ok := a.validateSSEHeaders(w, r, bucket, key)
		if !ok {
			return
		}

		// use stream copy via blob read->write pipeline.
		pr, pw := io.Pipe()
		errCh := make(chan error, 1)
		go func() {
			defer func() { _ = pw.Close() }()
			meta := blob.ObjectMeta{Ref: blob.ObjectRef{Bucket: srcObj.Bucket, Key: srcObj.Key, VersionID: srcObj.VersionID}, Path: srcObj.StoragePath, Size: srcObj.Size, SHA256: srcObj.ChecksumSHA256, MD5Hex: srcObj.ETag, CreatedAt: srcObj.CreatedAt}
			_, readErr := a.readMaybeEncryptedObject(r, meta, srcObj, nil, pw)
			errCh <- readErr
		}()

		dstBucket, err := a.store.GetBucket(r.Context(), bucket)
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
			return
		}
		versionID := "null"
		if dstBucket.VersioningStatus == "Enabled" {
			versionID = newVersionID()
		}
		if versionID == "null" {
			if existing, err := a.store.GetObjectVersion(r.Context(), bucket, key, "null"); err == nil && objectLocked(existing, time.Now().UTC()) {
				writeObjectLockedError(w, r)
				return
			}
		}
		ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID}
		blobMeta, err := a.writeMaybeEncryptedObject(r, ref, pr, encrypted)
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Copy object failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		if readErr := <-errCh; readErr != nil {
			_ = a.blob.DeleteObject(r.Context(), ref, true)
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Copy source read failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		objectMeta := metadata.ObjectVersion{
			Bucket:         bucket,
			Key:            key,
			VersionID:      versionID,
			Size:           blobMeta.Size,
			ETag:           blobMeta.MD5Hex,
			ChecksumSHA256: blobMeta.SHA256B64,
			ChecksumSHA1:   blobMeta.SHA1B64,
			ChecksumCRC32:  blobMeta.CRC32B64,
			ChecksumCRC32C: blobMeta.CRC32CB64,
			StoragePath:    blobMeta.Path,
			Metadata:       markSSEMetadata(copyMetadata(srcObj.Metadata), encrypted),
			CreatedAt:      blobMeta.CreatedAt,
		}
		if err := a.store.PutObjectVersion(r.Context(), objectMeta); err != nil {
			_ = a.blob.DeleteObject(r.Context(), ref, true)
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Copy metadata write failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		type copyResult struct {
			XMLName      xml.Name `xml:"CopyObjectResult"`
			ETag         string   `xml:"ETag"`
			LastModified string   `xml:"LastModified"`
		}
		writeXML(w, http.StatusOK, copyResult{ETag: quotedETag(blobMeta.MD5Hex), LastModified: blobMeta.CreatedAt.Format(time.RFC3339)})
		a.emitObjectNotification(r, "ObjectCreated:Copy", objectMeta)
		a.emitReplication(r, objectMeta)
	}) {
		return
	}
}

func (a *s3API) handleCreateMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := newVersionID()
	encrypted, ok := a.validateSSEHeaders(w, r, bucket, key)
	if !ok {
		return
	}
	sseAlgorithm := ""
	if encrypted {
		sseAlgorithm = sseAlgorithmAES256
	}
	if err := a.store.CreateMultipartUpload(r.Context(), metadata.MultipartUpload{
		UploadID:     uploadID,
		Bucket:       bucket,
		Key:          key,
		SSEAlgorithm: sseAlgorithm,
		InitiatedAt:  time.Now().UTC(),
	}); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Create multipart upload failed.", Resource: "/" + bucket + "/" + key})
		return
	}
	if err := a.blob.CreateMultipartUpload(r.Context(), uploadID); err != nil {
		_ = a.store.DeleteMultipartUpload(r.Context(), uploadID)
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Create multipart upload staging failed.", Resource: "/" + bucket + "/" + key})
		return
	}

	type result struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		UploadID string   `xml:"UploadId"`
	}
	if encrypted {
		w.Header().Set("x-amz-server-side-encryption", sseAlgorithmAES256)
	}
	writeXML(w, http.StatusOK, result{Bucket: bucket, Key: key, UploadID: uploadID})
}

func (a *s3API) handleUploadPart(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	if !a.runHeavy(w, r, bucket, key, func() {
		partNumber, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
		if err != nil || partNumber <= 0 || partNumber > maxMultipartParts {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidArgument", Message: "partNumber must be between 1 and 10000.", Resource: "/" + bucket + "/" + key})
			return
		}
		upload, _, err := a.store.GetMultipartUpload(r.Context(), uploadID)
		if err != nil || upload.Bucket != bucket || upload.Key != key {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchUpload", Message: "The specified multipart upload does not exist.", Resource: "/" + bucket + "/" + key})
			return
		}

		meta, err := a.blob.WriteMultipartPart(r.Context(), uploadID, partNumber, requestObjectBody(r))
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Upload part failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		if !validateChecksumHeaders(w, r, blob.ObjectMeta{CRC32B64: meta.CRC32B64, CRC32CB64: meta.CRC32CB64, SHA1B64: meta.SHA1B64, SHA256B64: meta.SHA256B64}) {
			_ = a.blob.AbortMultipartUpload(r.Context(), uploadID)
			return
		}
		if err := a.store.UpsertMultipartPart(r.Context(), metadata.MultipartPart{
			UploadID:       uploadID,
			PartNumber:     partNumber,
			ETag:           meta.MD5Hex,
			Size:           meta.Size,
			ChecksumSHA256: meta.SHA256B64,
			ChecksumSHA1:   meta.SHA1B64,
			ChecksumCRC32:  meta.CRC32B64,
			ChecksumCRC32C: meta.CRC32CB64,
			StoragePath:    meta.Path,
			CreatedAt:      meta.CreatedAt,
		}); err != nil {
			_ = a.blob.AbortMultipartUpload(r.Context(), uploadID)
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Upload part metadata failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		w.Header().Set("ETag", quotedETag(meta.MD5Hex))
		setObjectChecksumHeaders(w.Header(), metadata.ObjectVersion{ChecksumSHA256: meta.SHA256B64, ChecksumSHA1: meta.SHA1B64, ChecksumCRC32: meta.CRC32B64, ChecksumCRC32C: meta.CRC32CB64})
		w.WriteHeader(http.StatusOK)
	}) {
		return
	}
}

func (a *s3API) handleListParts(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	upload, parts, err := a.store.GetMultipartUpload(r.Context(), uploadID)
	if err != nil || upload.Bucket != bucket || upload.Key != key {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchUpload", Message: "The specified multipart upload does not exist.", Resource: "/" + bucket + "/" + key})
		return
	}
	marker := 0
	if raw := r.URL.Query().Get("part-number-marker"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidArgument", Message: "Invalid part-number-marker.", Resource: "/" + bucket + "/" + key})
			return
		}
		marker = parsed
	}
	maxParts := 1000
	if raw := r.URL.Query().Get("max-parts"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 || parsed > 1000 {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidArgument", Message: "Invalid max-parts.", Resource: "/" + bucket + "/" + key})
			return
		}
		maxParts = parsed
	}
	type partItem struct {
		PartNumber     int    `xml:"PartNumber"`
		ETag           string `xml:"ETag"`
		Size           int64  `xml:"Size"`
		ChecksumCRC32  string `xml:"ChecksumCRC32,omitempty"`
		ChecksumCRC32C string `xml:"ChecksumCRC32C,omitempty"`
		ChecksumSHA1   string `xml:"ChecksumSHA1,omitempty"`
		ChecksumSHA256 string `xml:"ChecksumSHA256,omitempty"`
	}
	type result struct {
		XMLName              xml.Name   `xml:"ListPartsResult"`
		Bucket               string     `xml:"Bucket"`
		Key                  string     `xml:"Key"`
		UploadID             string     `xml:"UploadId"`
		PartNumberMarker     int        `xml:"PartNumberMarker"`
		NextPartNumberMarker int        `xml:"NextPartNumberMarker,omitempty"`
		MaxParts             int        `xml:"MaxParts"`
		IsTruncated          bool       `xml:"IsTruncated"`
		Parts                []partItem `xml:"Part"`
	}
	out := result{Bucket: bucket, Key: key, UploadID: uploadID, PartNumberMarker: marker, MaxParts: maxParts}
	if maxParts == 0 {
		out.IsTruncated = len(parts) > 0
		writeXML(w, http.StatusOK, out)
		return
	}
	for _, p := range parts {
		if p.PartNumber <= marker {
			continue
		}
		if len(out.Parts) == maxParts {
			out.IsTruncated = true
			out.NextPartNumberMarker = out.Parts[len(out.Parts)-1].PartNumber
			break
		}
		out.Parts = append(out.Parts, partItem{PartNumber: p.PartNumber, ETag: quotedETag(p.ETag), Size: p.Size, ChecksumCRC32: p.ChecksumCRC32, ChecksumCRC32C: p.ChecksumCRC32C, ChecksumSHA1: p.ChecksumSHA1, ChecksumSHA256: p.ChecksumSHA256})
	}
	writeXML(w, http.StatusOK, out)
}

func (a *s3API) handleCompleteMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	if !a.runHeavy(w, r, bucket, key, func() {
		upload, storedParts, err := a.store.GetMultipartUpload(r.Context(), uploadID)
		if err != nil || upload.Bucket != bucket || upload.Key != key {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchUpload", Message: "The specified multipart upload does not exist.", Resource: "/" + bucket + "/" + key})
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "Could not read request body.", Resource: "/" + bucket + "/" + key})
			return
		}
		var req struct {
			Parts []struct {
				PartNumber int    `xml:"PartNumber"`
				ETag       string `xml:"ETag"`
			} `xml:"Part"`
		}
		if err := xml.Unmarshal(body, &req); err != nil || len(req.Parts) == 0 {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket + "/" + key})
			return
		}
		if len(req.Parts) > maxMultipartParts {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "TooManyParts", Message: "Multipart uploads cannot contain more than 10000 parts.", Resource: "/" + bucket + "/" + key})
			return
		}

		storedByNumber := map[int]metadata.MultipartPart{}
		for _, part := range storedParts {
			storedByNumber[part.PartNumber] = part
		}
		orderedParts := make([]int, 0, len(req.Parts))
		for i, part := range req.Parts {
			if i > 0 && part.PartNumber <= req.Parts[i-1].PartNumber {
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidPartOrder", Message: "The list of parts was not in ascending order.", Resource: "/" + bucket + "/" + key})
				return
			}
			stored, ok := storedByNumber[part.PartNumber]
			if !ok {
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidPart", Message: "One or more of the specified parts could not be found.", Resource: "/" + bucket + "/" + key})
				return
			}
			if strings.Trim(part.ETag, "\"") != stored.ETag {
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidPart", Message: "One or more of the specified part ETags did not match.", Resource: "/" + bucket + "/" + key})
				return
			}
			if i < len(req.Parts)-1 && stored.Size < minMultipartPartSize {
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "EntityTooSmall", Message: "Your proposed upload is smaller than the minimum allowed object size.", Resource: "/" + bucket + "/" + key})
				return
			}
			orderedParts = append(orderedParts, part.PartNumber)
		}

		b, err := a.store.GetBucket(r.Context(), bucket)
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
			return
		}
		versionID := "null"
		if b.VersioningStatus == "Enabled" {
			versionID = newVersionID()
		}
		if versionID == "null" {
			if existing, err := a.store.GetObjectVersion(r.Context(), bucket, key, "null"); err == nil && objectLocked(existing, time.Now().UTC()) {
				writeObjectLockedError(w, r)
				return
			}
		}
		ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID}
		var objMeta blob.ObjectMeta
		encrypted := upload.SSEAlgorithm == sseAlgorithmAES256
		if encrypted {
			objMeta, err = a.blob.CompleteMultipartUploadWithWriter(r.Context(), uploadID, orderedParts, func(src io.Reader) (blob.ObjectMeta, error) {
				return a.writeMaybeEncryptedObject(r, ref, src, true)
			})
		} else if len(orderedParts) == 1 {
			stored := storedByNumber[orderedParts[0]]
			objMeta, err = a.blob.PromoteMultipartPart(r.Context(), uploadID, stored.PartNumber, ref, blob.MultipartPartMeta{Size: stored.Size, MD5Hex: stored.ETag, SHA256B64: stored.ChecksumSHA256, SHA1B64: stored.ChecksumSHA1, CRC32B64: stored.ChecksumCRC32, CRC32CB64: stored.ChecksumCRC32C, CreatedAt: stored.CreatedAt})
		} else {
			objMeta, err = a.blob.CompleteMultipartUpload(r.Context(), uploadID, orderedParts, ref)
		}
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Complete multipart upload failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		partETags := make([]string, 0, len(req.Parts))
		for _, part := range req.Parts {
			partETags = append(partETags, strings.Trim(part.ETag, "\""))
		}
		multipartETag := calculateMultipartETag(partETags)
		objectMeta := metadata.ObjectVersion{
			Bucket:         bucket,
			Key:            key,
			VersionID:      versionID,
			Size:           objMeta.Size,
			ETag:           multipartETag,
			ChecksumSHA256: objMeta.SHA256B64,
			ChecksumSHA1:   objMeta.SHA1B64,
			ChecksumCRC32:  objMeta.CRC32B64,
			ChecksumCRC32C: objMeta.CRC32CB64,
			StoragePath:    objMeta.Path,
			Metadata:       markSSEMetadata(nil, encrypted),
			CreatedAt:      objMeta.CreatedAt,
		}
		if err := a.store.PutObjectVersion(r.Context(), objectMeta); err != nil {
			_ = a.blob.DeleteObject(r.Context(), ref, true)
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Complete multipart metadata write failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		_ = a.store.DeleteMultipartUpload(r.Context(), uploadID)

		type result struct {
			XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
			Location string   `xml:"Location"`
			Bucket   string   `xml:"Bucket"`
			Key      string   `xml:"Key"`
			ETag     string   `xml:"ETag"`
			CRC32    string   `xml:"ChecksumCRC32,omitempty"`
			CRC32C   string   `xml:"ChecksumCRC32C,omitempty"`
			SHA1     string   `xml:"ChecksumSHA1,omitempty"`
			SHA256   string   `xml:"ChecksumSHA256,omitempty"`
		}
		setObjectChecksumHeaders(w.Header(), metadata.ObjectVersion{ChecksumSHA256: objMeta.SHA256B64, ChecksumSHA1: objMeta.SHA1B64, ChecksumCRC32: objMeta.CRC32B64, ChecksumCRC32C: objMeta.CRC32CB64})
		setSSEHeader(w.Header(), objectMeta)
		writeXML(w, http.StatusOK, result{Location: "/" + bucket + "/" + key, Bucket: bucket, Key: key, ETag: quotedETag(multipartETag), CRC32: objMeta.CRC32B64, CRC32C: objMeta.CRC32CB64, SHA1: objMeta.SHA1B64, SHA256: objMeta.SHA256B64})
		a.emitObjectNotification(r, "ObjectCreated:CompleteMultipartUpload", objectMeta)
		a.emitReplication(r, objectMeta)
	}) {
		return
	}
}

func (a *s3API) runHeavy(w http.ResponseWriter, r *http.Request, bucket, key string, fn func()) bool {
	if a.heavy == nil {
		fn()
		return true
	}
	if err := a.heavy.Acquire(r.Context()); err != nil {
		writeS3Error(w, r, s3ErrorSpec{
			StatusCode: http.StatusServiceUnavailable,
			Code:       "SlowDown",
			Message:    "Server is overloaded. Please retry shortly.",
			Resource:   "/" + bucket + "/" + key,
		})
		return false
	}
	defer a.heavy.Release()
	fn()
	return true
}

func (a *s3API) handleAbortMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	upload, _, err := a.store.GetMultipartUpload(r.Context(), uploadID)
	if err != nil || upload.Bucket != bucket || upload.Key != key {
		a.emitAudit(r, "multipart.abort", "allow", bucket, key, "")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = a.blob.AbortMultipartUpload(r.Context(), uploadID)
	_ = a.store.DeleteMultipartUpload(r.Context(), uploadID)
	a.emitAudit(r, "multipart.abort", "allow", bucket, key, "")
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) emitAudit(r *http.Request, action, outcome, bucket, key, reason string) {
	if !a.auditEnabled {
		return
	}
	sink := a.audit
	if sink == nil {
		sink = slogAuditSink{}
	}
	sink.Emit(AuditEvent{
		At:        time.Now().UTC(),
		Action:    action,
		Outcome:   outcome,
		RequestID: RequestIDFromContext(r.Context()),
		Principal: PrincipalFromContext(r.Context()),
		Bucket:    bucket,
		Key:       key,
		Reason:    reason,
	})
}

func (a *s3API) handleListMultipartUploads(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	uploads, err := a.store.ListMultipartUploads(r.Context(), bucket, q.Get("prefix"))
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	keyMarker := q.Get("key-marker")
	uploadIDMarker := q.Get("upload-id-marker")
	maxUploads := 1000
	if raw := q.Get("max-uploads"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 || parsed > 1000 {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidArgument", Message: "Invalid max-uploads.", Resource: "/" + bucket})
			return
		}
		maxUploads = parsed
	}
	type uploadEntry struct {
		Key       string `xml:"Key"`
		UploadID  string `xml:"UploadId"`
		Initiated string `xml:"Initiated"`
	}
	type result struct {
		XMLName            xml.Name      `xml:"ListMultipartUploadsResult"`
		Bucket             string        `xml:"Bucket"`
		KeyMarker          string        `xml:"KeyMarker,omitempty"`
		UploadIDMarker     string        `xml:"UploadIdMarker,omitempty"`
		NextKeyMarker      string        `xml:"NextKeyMarker,omitempty"`
		NextUploadIDMarker string        `xml:"NextUploadIdMarker,omitempty"`
		MaxUploads         int           `xml:"MaxUploads"`
		IsTruncated        bool          `xml:"IsTruncated"`
		Uploads            []uploadEntry `xml:"Upload"`
	}
	out := result{Bucket: bucket, KeyMarker: keyMarker, UploadIDMarker: uploadIDMarker, MaxUploads: maxUploads}
	if maxUploads == 0 {
		out.IsTruncated = len(uploads) > 0
		writeXML(w, http.StatusOK, out)
		return
	}
	for _, upload := range uploads {
		if keyMarker != "" {
			if upload.Key < keyMarker || (upload.Key == keyMarker && upload.UploadID <= uploadIDMarker) {
				continue
			}
		}
		if len(out.Uploads) == maxUploads {
			out.IsTruncated = true
			last := out.Uploads[len(out.Uploads)-1]
			out.NextKeyMarker = last.Key
			out.NextUploadIDMarker = last.UploadID
			break
		}
		out.Uploads = append(out.Uploads, uploadEntry{Key: upload.Key, UploadID: upload.UploadID, Initiated: upload.InitiatedAt.Format(time.RFC3339)})
	}
	writeXML(w, http.StatusOK, out)
}

func writeXML(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(payload)
}

func writeACLXML(w http.ResponseWriter) {
	type owner struct {
		ID          string `xml:"ID"`
		DisplayName string `xml:"DisplayName"`
	}
	type grantee struct {
		XMLNS       string `xml:"xmlns:xsi,attr"`
		Type        string `xml:"xsi:type,attr"`
		ID          string `xml:"ID"`
		DisplayName string `xml:"DisplayName"`
	}
	type grant struct {
		Grantee    grantee `xml:"Grantee"`
		Permission string  `xml:"Permission"`
	}
	type acl struct {
		XMLName xml.Name `xml:"AccessControlPolicy"`
		Owner   owner    `xml:"Owner"`
		Grants  []grant  `xml:"AccessControlList>Grant"`
	}
	ownerValue := owner{ID: "s000-owner", DisplayName: "s000"}
	writeXML(w, http.StatusOK, acl{Owner: ownerValue, Grants: []grant{{Grantee: grantee{XMLNS: "http://www.w3.org/2001/XMLSchema-instance", Type: "CanonicalUser", ID: ownerValue.ID, DisplayName: ownerValue.DisplayName}, Permission: "FULL_CONTROL"}}})
}

func isSupportedCannedACL(value string) bool {
	switch strings.ToLower(value) {
	case "private", "public-read", "public-read-write", "authenticated-read", "bucket-owner-read", "bucket-owner-full-control":
		return true
	default:
		return false
	}
}

func hasQueryFlag(values map[string][]string, key string) bool {
	_, ok := values[key]
	return ok
}

func collectUserMetadata(h http.Header) map[string]string {
	out := map[string]string{}
	for k, values := range h {
		low := strings.ToLower(k)
		if !strings.HasPrefix(low, "x-amz-meta-") {
			continue
		}
		name := strings.TrimPrefix(low, "x-amz-meta-")
		out[name] = strings.Join(values, ",")
	}
	return out
}

func copyMetadata(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func isWellFormedXML(value []byte) bool {
	dec := xml.NewDecoder(strings.NewReader(string(value)))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}

func objectConditionsPass(w http.ResponseWriter, r *http.Request, obj metadata.ObjectVersion, prefix string) bool {
	if match := r.Header.Get(prefix + "if-match"); match != "" && !etagListMatches(match, obj.ETag) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusPreconditionFailed, Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold.", Resource: r.URL.Path})
		return false
	}
	if noneMatch := r.Header.Get(prefix + "if-none-match"); noneMatch != "" && etagListMatches(noneMatch, obj.ETag) {
		if prefix == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			w.WriteHeader(http.StatusNotModified)
		} else {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusPreconditionFailed, Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold.", Resource: r.URL.Path})
		}
		return false
	}
	if since := r.Header.Get(prefix + "if-modified-since"); since != "" {
		when, err := http.ParseTime(since)
		if err == nil && !obj.CreatedAt.After(when) {
			if prefix == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				w.WriteHeader(http.StatusNotModified)
			} else {
				writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusPreconditionFailed, Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold.", Resource: r.URL.Path})
			}
			return false
		}
	}
	if unmodifiedSince := r.Header.Get(prefix + "if-unmodified-since"); unmodifiedSince != "" {
		when, err := http.ParseTime(unmodifiedSince)
		if err == nil && obj.CreatedAt.After(when) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusPreconditionFailed, Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold.", Resource: r.URL.Path})
			return false
		}
	}
	return true
}

func validateChecksumHeaders(w http.ResponseWriter, r *http.Request, meta blob.ObjectMeta) bool {
	checks := map[string]string{
		"x-amz-checksum-crc32":  meta.CRC32B64,
		"x-amz-checksum-crc32c": meta.CRC32CB64,
		"x-amz-checksum-sha1":   meta.SHA1B64,
		"x-amz-checksum-sha256": meta.SHA256B64,
	}
	for header, actual := range checks {
		expected := strings.TrimSpace(r.Header.Get(header))
		if expected == "" {
			continue
		}
		if expected != actual {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "BadDigest", Message: "The checksum you specified did not match what we received.", Resource: r.URL.Path})
			return false
		}
	}
	return true
}

func setObjectChecksumHeaders(h http.Header, obj metadata.ObjectVersion) {
	if obj.ChecksumCRC32 != "" {
		h.Set("x-amz-checksum-crc32", obj.ChecksumCRC32)
	}
	if obj.ChecksumCRC32C != "" {
		h.Set("x-amz-checksum-crc32c", obj.ChecksumCRC32C)
	}
	if obj.ChecksumSHA1 != "" {
		h.Set("x-amz-checksum-sha1", obj.ChecksumSHA1)
	}
	if obj.ChecksumSHA256 != "" {
		h.Set("x-amz-checksum-sha256", obj.ChecksumSHA256)
	}
}

func calculateMultipartETag(partETags []string) string {
	h := md5.New()
	for _, etag := range partETags {
		decoded, err := hex.DecodeString(strings.Trim(etag, "\""))
		if err != nil {
			continue
		}
		_, _ = h.Write(decoded)
	}
	return hex.EncodeToString(h.Sum(nil)) + "-" + strconv.Itoa(len(partETags))
}

func etagListMatches(value string, etag string) bool {
	value = strings.TrimSpace(value)
	if value == "*" {
		return true
	}
	for _, part := range strings.Split(value, ",") {
		if strings.Trim(strings.TrimSpace(part), "\"") == etag {
			return true
		}
	}
	return false
}

func newVersionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), hex.EncodeToString(b))
}

func quotedETag(etag string) string {
	if strings.HasPrefix(etag, "\"") && strings.HasSuffix(etag, "\"") {
		return etag
	}
	return "\"" + etag + "\""
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func parseRange(header string, size int64) (*blob.ByteRange, error) {
	if !strings.HasPrefix(header, "bytes=") {
		return nil, fmt.Errorf("invalid range")
	}
	parts := strings.SplitN(strings.TrimPrefix(header, "bytes="), "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid range")
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, err
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, err
	}
	if start < 0 || end < start || end >= size {
		return nil, fmt.Errorf("invalid range")
	}
	return &blob.ByteRange{Start: start, End: end}, nil
}
