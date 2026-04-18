package functions

import "time"

const (
	EventTypeS3   = "s3"
	EventTypeHTTP = "http"
	EventTypeCron = "cron"

	TriggerHTTPPre  = "onHTTPPre"
	TriggerHTTPPost = "onHTTPPost"
	TriggerCronTick = "onCronTick"
)

type S3Event struct {
	Type      string    `json:"type"`
	Operation string    `json:"operation"`
	Phase     string    `json:"phase"`
	Bucket    string    `json:"bucket"`
	Key       string    `json:"key"`
	Size      int64     `json:"size,omitempty"`
	Method    string    `json:"method,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type HTTPEvent struct {
	Type      string    `json:"type"`
	Phase     string    `json:"phase"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type CronEvent struct {
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Scheduled time.Time `json:"scheduled"`
}
