package server

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"ds9labs.com/s000/internal/metadata"
)

type notificationConfigurationXML struct {
	XMLName xml.Name                `xml:"NotificationConfiguration"`
	Queues  []notificationTargetXML `xml:"QueueConfiguration"`
	Topics  []notificationTargetXML `xml:"TopicConfiguration"`
	Lambdas []notificationTargetXML `xml:"CloudFunctionConfiguration"`
}

type notificationTargetXML struct {
	ID       string   `xml:"Id"`
	Queue    string   `xml:"Queue"`
	Topic    string   `xml:"Topic"`
	Function string   `xml:"CloudFunction"`
	Endpoint string   `xml:"Endpoint"`
	URL      string   `xml:"Url"`
	Events   []string `xml:"Event"`
}

type s3NotificationEnvelope struct {
	Records []s3NotificationRecord `json:"Records"`
}

type s3NotificationRecord struct {
	EventVersion string `json:"eventVersion"`
	EventSource  string `json:"eventSource"`
	EventTime    string `json:"eventTime"`
	EventName    string `json:"eventName"`
	S3           struct {
		Bucket struct {
			Name string `json:"name"`
		} `json:"bucket"`
		Object struct {
			Key       string `json:"key"`
			Size      int64  `json:"size,omitempty"`
			ETag      string `json:"eTag,omitempty"`
			VersionID string `json:"versionId,omitempty"`
		} `json:"object"`
	} `json:"s3"`
}

func (a *s3API) handlePutBucketNotification(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	body, err := ioReadAllLimit(r.Body, 1024*1024)
	if err != nil || !isWellFormedXML(body) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
		return
	}
	var cfg notificationConfigurationXML
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := xml.Unmarshal(body, &cfg); err != nil {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
			return
		}
	}
	if err := a.store.PutBucketNotification(r.Context(), metadata.BucketNotification{Bucket: bucket, Document: string(body), Enabled: true}); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put bucket notification failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleGetBucketNotification(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := a.store.GetBucketNotification(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			writeXML(w, http.StatusOK, notificationConfigurationXML{})
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Get bucket notification failed.", Resource: "/" + bucket})
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(cfg.Document))
}

func (a *s3API) handleDeleteBucketNotification(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := a.store.DeleteBucketNotification(r.Context(), bucket); err != nil && !errors.Is(err, metadata.ErrNotFound) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete bucket notification failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) emitObjectNotification(r *http.Request, eventName string, obj metadata.ObjectVersion) {
	if a.store == nil {
		return
	}
	cfg, err := a.store.GetBucketNotification(r.Context(), obj.Bucket)
	if err != nil || !cfg.Enabled || strings.TrimSpace(cfg.Document) == "" {
		return
	}
	var parsed notificationConfigurationXML
	if err := xml.Unmarshal([]byte(cfg.Document), &parsed); err != nil {
		return
	}
	payload, err := json.Marshal(notificationEnvelope(eventName, obj))
	if err != nil {
		return
	}
	for _, target := range notificationTargets(parsed) {
		endpoint := targetEndpoint(target)
		if endpoint == "" || !notificationEventMatches(target.Events, eventName) {
			continue
		}
		go postNotification(endpoint, payload)
	}
}

func notificationTargets(cfg notificationConfigurationXML) []notificationTargetXML {
	out := make([]notificationTargetXML, 0, len(cfg.Queues)+len(cfg.Topics)+len(cfg.Lambdas))
	out = append(out, cfg.Queues...)
	out = append(out, cfg.Topics...)
	out = append(out, cfg.Lambdas...)
	return out
}

func targetEndpoint(target notificationTargetXML) string {
	for _, value := range []string{target.Endpoint, target.URL, target.Queue, target.Topic, target.Function} {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			return value
		}
	}
	return ""
}

func notificationEventMatches(events []string, eventName string) bool {
	if len(events) == 0 {
		return true
	}
	for _, event := range events {
		event = strings.TrimSpace(event)
		if event == eventName || strings.HasSuffix(event, ":*") && strings.HasPrefix(eventName, strings.TrimSuffix(event, "*")) {
			return true
		}
	}
	return false
}

func notificationEnvelope(eventName string, obj metadata.ObjectVersion) s3NotificationEnvelope {
	record := s3NotificationRecord{EventVersion: "2.1", EventSource: "aws:s3", EventTime: time.Now().UTC().Format(time.RFC3339), EventName: eventName}
	record.S3.Bucket.Name = obj.Bucket
	record.S3.Object.Key = obj.Key
	record.S3.Object.Size = obj.Size
	record.S3.Object.ETag = obj.ETag
	record.S3.Object.VersionID = obj.VersionID
	return s3NotificationEnvelope{Records: []s3NotificationRecord{record}}
}

func postNotification(endpoint string, payload []byte) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(payload))
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
}

func ioReadAllLimit(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit+1))
}
