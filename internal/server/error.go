package server

import (
	"encoding/xml"
	"net/http"
)

type s3ErrorSpec struct {
	StatusCode int
	Code       string
	Message    string
	Resource   string
}

type s3ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
}

func writeS3Error(w http.ResponseWriter, r *http.Request, spec s3ErrorSpec) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(spec.StatusCode)

	_ = xml.NewEncoder(w).Encode(s3ErrorResponse{
		Code:      spec.Code,
		Message:   spec.Message,
		Resource:  spec.Resource,
		RequestID: RequestIDFromContext(r.Context()),
	})
}
