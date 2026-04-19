package server

import (
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

type s3API struct {
	domain       string
	store        metadata.Store
	blob         *blob.Store
	region       string
	heavy        *heavyOpLimiter
	auditEnabled bool
	audit        AuditSink
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
	case r.Method == http.MethodPut && hasQueryFlag(q, "versioning"):
		a.handlePutBucketVersioning(w, r, bucket)
	case r.Method == http.MethodGet && hasQueryFlag(q, "versioning"):
		a.handleGetBucketVersioning(w, r, bucket)
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
	objects, err := a.store.ListObjects(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
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

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	startAfter, err := decodeListContinuationToken(continuation, prefix, delimiter)
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidArgument", Message: "Invalid continuation-token.", Resource: "/" + bucket})
		return
	}

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
		ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID}
		blobMeta, err := a.blob.WriteObject(r.Context(), ref, r.Body)
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

		if err := a.store.PutObjectVersion(r.Context(), metadata.ObjectVersion{
			Bucket:         bucket,
			Key:            key,
			VersionID:      versionID,
			Size:           blobMeta.Size,
			ETag:           blobMeta.MD5Hex,
			ChecksumSHA256: blobMeta.SHA256,
			StoragePath:    blobMeta.Path,
			Metadata:       collectUserMetadata(r.Header),
			CreatedAt:      blobMeta.CreatedAt,
		}); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Metadata write failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		w.Header().Set("ETag", quotedETag(blobMeta.MD5Hex))
		w.Header().Set("x-amz-checksum-sha256", blobMeta.SHA256)
		w.WriteHeader(http.StatusOK)
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

	w.Header().Set("ETag", quotedETag(obj.ETag))
	w.Header().Set("x-amz-checksum-sha256", obj.ChecksumSHA256)
	for k, v := range obj.Metadata {
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
		_, err = a.blob.ReadObject(r.Context(), meta, br, w)
	} else {
		w.WriteHeader(http.StatusOK)
		_, err = a.blob.ReadObject(r.Context(), meta, nil, w)
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
	if b.VersioningStatus == "Enabled" {
		_ = a.store.DeleteObject(r.Context(), bucket, key, newVersionID(), time.Now().UTC())
		a.emitAudit(r, "object.delete", "allow", bucket, key, "")
		w.WriteHeader(http.StatusNoContent)
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
	}
	a.emitAudit(r, "object.delete", "allow", bucket, key, "")
	w.WriteHeader(http.StatusNoContent)
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

		// use stream copy via blob read->write pipeline.
		pr, pw := io.Pipe()
		errCh := make(chan error, 1)
		go func() {
			defer func() { _ = pw.Close() }()
			meta := blob.ObjectMeta{Ref: blob.ObjectRef{Bucket: srcObj.Bucket, Key: srcObj.Key, VersionID: srcObj.VersionID}, Path: srcObj.StoragePath, Size: srcObj.Size, SHA256: srcObj.ChecksumSHA256, MD5Hex: srcObj.ETag, CreatedAt: srcObj.CreatedAt}
			_, readErr := a.blob.ReadObject(r.Context(), meta, nil, pw)
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
		ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID}
		blobMeta, err := a.blob.WriteObject(r.Context(), ref, pr)
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Copy object failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		if readErr := <-errCh; readErr != nil {
			_ = a.blob.DeleteObject(r.Context(), ref, true)
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Copy source read failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		if err := a.store.PutObjectVersion(r.Context(), metadata.ObjectVersion{
			Bucket:         bucket,
			Key:            key,
			VersionID:      versionID,
			Size:           blobMeta.Size,
			ETag:           blobMeta.MD5Hex,
			ChecksumSHA256: blobMeta.SHA256,
			StoragePath:    blobMeta.Path,
			Metadata:       copyMetadata(srcObj.Metadata),
			CreatedAt:      blobMeta.CreatedAt,
		}); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Copy metadata write failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		type copyResult struct {
			XMLName      xml.Name `xml:"CopyObjectResult"`
			ETag         string   `xml:"ETag"`
			LastModified string   `xml:"LastModified"`
		}
		writeXML(w, http.StatusOK, copyResult{ETag: quotedETag(blobMeta.MD5Hex), LastModified: blobMeta.CreatedAt.Format(time.RFC3339)})
	}) {
		return
	}
}

func (a *s3API) handleCreateMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := newVersionID()
	if err := a.store.CreateMultipartUpload(r.Context(), metadata.MultipartUpload{
		UploadID:    uploadID,
		Bucket:      bucket,
		Key:         key,
		InitiatedAt: time.Now().UTC(),
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
	writeXML(w, http.StatusOK, result{Bucket: bucket, Key: key, UploadID: uploadID})
}

func (a *s3API) handleUploadPart(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	if !a.runHeavy(w, r, bucket, key, func() {
		partNumber, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
		if err != nil || partNumber <= 0 {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "partNumber must be a positive integer.", Resource: "/" + bucket + "/" + key})
			return
		}
		upload, _, err := a.store.GetMultipartUpload(r.Context(), uploadID)
		if err != nil || upload.Bucket != bucket || upload.Key != key {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchUpload", Message: "The specified multipart upload does not exist.", Resource: "/" + bucket + "/" + key})
			return
		}

		meta, err := a.blob.WriteMultipartPart(r.Context(), uploadID, partNumber, r.Body)
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Upload part failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		if err := a.store.UpsertMultipartPart(r.Context(), metadata.MultipartPart{
			UploadID:       uploadID,
			PartNumber:     partNumber,
			ETag:           meta.MD5Hex,
			Size:           meta.Size,
			ChecksumSHA256: meta.SHA256,
			StoragePath:    meta.Path,
			CreatedAt:      meta.CreatedAt,
		}); err != nil {
			_ = a.blob.AbortMultipartUpload(r.Context(), uploadID)
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Upload part metadata failed.", Resource: "/" + bucket + "/" + key})
			return
		}

		w.Header().Set("ETag", quotedETag(meta.MD5Hex))
		w.Header().Set("x-amz-checksum-sha256", meta.SHA256)
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
	type partItem struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
		Size       int64  `xml:"Size"`
	}
	type result struct {
		XMLName  xml.Name   `xml:"ListPartsResult"`
		Bucket   string     `xml:"Bucket"`
		Key      string     `xml:"Key"`
		UploadID string     `xml:"UploadId"`
		Parts    []partItem `xml:"Part"`
	}
	out := result{Bucket: bucket, Key: key, UploadID: uploadID}
	for _, p := range parts {
		out.Parts = append(out.Parts, partItem{PartNumber: p.PartNumber, ETag: quotedETag(p.ETag), Size: p.Size})
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
		objMeta, err := a.blob.CompleteMultipartUpload(r.Context(), uploadID, orderedParts, blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID})
		if err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Complete multipart upload failed.", Resource: "/" + bucket + "/" + key})
			return
		}
		if err := a.store.PutObjectVersion(r.Context(), metadata.ObjectVersion{
			Bucket:         bucket,
			Key:            key,
			VersionID:      versionID,
			Size:           objMeta.Size,
			ETag:           objMeta.MD5Hex,
			ChecksumSHA256: objMeta.SHA256,
			StoragePath:    objMeta.Path,
			CreatedAt:      objMeta.CreatedAt,
		}); err != nil {
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
			Checksum string   `xml:"ChecksumSHA256"`
		}
		writeXML(w, http.StatusOK, result{Location: "/" + bucket + "/" + key, Bucket: bucket, Key: key, ETag: quotedETag(objMeta.MD5Hex), Checksum: objMeta.SHA256})
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
	uploads, err := a.store.ListMultipartUploads(r.Context(), bucket, r.URL.Query().Get("prefix"))
	if err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	type uploadEntry struct {
		Key       string `xml:"Key"`
		UploadID  string `xml:"UploadId"`
		Initiated string `xml:"Initiated"`
	}
	type result struct {
		XMLName xml.Name      `xml:"ListMultipartUploadsResult"`
		Bucket  string        `xml:"Bucket"`
		Uploads []uploadEntry `xml:"Upload"`
	}
	out := result{Bucket: bucket}
	for _, upload := range uploads {
		out.Uploads = append(out.Uploads, uploadEntry{Key: upload.Key, UploadID: upload.UploadID, Initiated: upload.InitiatedAt.Format(time.RFC3339)})
	}
	writeXML(w, http.StatusOK, out)
}

func writeXML(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(payload)
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
