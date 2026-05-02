package server

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"ds9labs.com/s000/internal/metadata"
)

const (
	lockRetentionModeKey        = "__s000_retention_mode"
	lockRetentionRetainUntilKey = "__s000_retention_retain_until"
	lockLegalHoldKey            = "__s000_legal_hold"
)

func objectLocked(obj metadata.ObjectVersion, now time.Time) bool {
	if obj.Metadata == nil {
		return false
	}
	if strings.EqualFold(obj.Metadata[lockLegalHoldKey], "ON") {
		return true
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(obj.Metadata[lockRetentionRetainUntilKey]))
	return err == nil && now.Before(until)
}

func writeObjectLockedError(w http.ResponseWriter, r *http.Request) {
	writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusForbidden, Code: "AccessDenied", Message: "Object is protected by object lock retention or legal hold.", Resource: r.URL.Path})
}

func retentionHeaders(obj metadata.ObjectVersion, h http.Header) {
	if obj.Metadata == nil {
		return
	}
	if mode := strings.TrimSpace(obj.Metadata[lockRetentionModeKey]); mode != "" {
		h.Set("x-amz-object-lock-mode", mode)
	}
	if until := strings.TrimSpace(obj.Metadata[lockRetentionRetainUntilKey]); until != "" {
		h.Set("x-amz-object-lock-retain-until-date", until)
	}
	if hold := strings.TrimSpace(obj.Metadata[lockLegalHoldKey]); hold != "" {
		h.Set("x-amz-object-lock-legal-hold", hold)
	}
}

func putRetentionMetadata(obj metadata.ObjectVersion, mode string, retainUntil time.Time) map[string]string {
	meta := copyMetadata(obj.Metadata)
	meta[lockRetentionModeKey] = mode
	meta[lockRetentionRetainUntilKey] = retainUntil.UTC().Format(time.RFC3339)
	return meta
}

func putLegalHoldMetadata(obj metadata.ObjectVersion, status string) map[string]string {
	meta := copyMetadata(obj.Metadata)
	meta[lockLegalHoldKey] = status
	return meta
}

type objectRetentionXML struct {
	XMLName         xml.Name `xml:"Retention"`
	Mode            string   `xml:"Mode,omitempty"`
	RetainUntilDate string   `xml:"RetainUntilDate,omitempty"`
}

type objectLegalHoldXML struct {
	XMLName xml.Name `xml:"LegalHold"`
	Status  string   `xml:"Status,omitempty"`
}
