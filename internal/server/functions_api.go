package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"ds9labs.com/s000/internal/functions"
)

type functionRequest struct {
	Name         string `json:"name"`
	Runtime      string `json:"runtime"`
	Trigger      string `json:"trigger"`
	Priority     int    `json:"priority"`
	Enabled      bool   `json:"enabled"`
	ModuleBase64 string `json:"module_base64"`
}

type activateVersionRequest struct {
	Version int `json:"version"`
}

type invokeRequest struct {
	Payload json.RawMessage `json:"payload"`
}

type functionHTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body"`
}

func functionsTemplatesHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mgr := opts.Functions
		if mgr == nil || !mgr.Enabled() {
			http.Error(w, `{"error":"functions runtime is disabled"}`, http.StatusServiceUnavailable)
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"templates": functions.BuiltinTemplates()})
	}
}

func functionsMetricsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mgr := opts.Functions
		if mgr == nil || !mgr.Enabled() {
			http.Error(w, `{"error":"functions runtime is disabled"}`, http.StatusServiceUnavailable)
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"metrics": mgr.Metrics()})
	}
}

func functionsAlertsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mgr := opts.Functions
		if mgr == nil || !mgr.Enabled() {
			http.Error(w, `{"error":"functions runtime is disabled"}`, http.StatusServiceUnavailable)
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"alerts": mgr.Alerts()})
	}
}

func functionsLogsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mgr := opts.Functions
		if mgr == nil || !mgr.Enabled() {
			http.Error(w, `{"error":"functions runtime is disabled"}`, http.StatusServiceUnavailable)
			return
		}
		limit := 50
		if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 {
				limit = n
			}
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"logs": mgr.RecentLogs(limit)})
	}
}

func functionsCollectionHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mgr := opts.Functions
		if mgr == nil || !mgr.Enabled() {
			http.Error(w, `{"error":"functions runtime is disabled"}`, http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSONResponse(w, http.StatusOK, map[string]any{"functions": mgr.ListFunctions()})
		case http.MethodPost:
			var req functionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
				return
			}
			module, err := base64.StdEncoding.DecodeString(req.ModuleBase64)
			if err != nil {
				http.Error(w, `{"error":"invalid module_base64"}`, http.StatusBadRequest)
				return
			}
			if req.Runtime == "" {
				req.Runtime = functions.RuntimeWazero
			}
			if err := mgr.CreateFunction(functions.Function{
				Name:     req.Name,
				Runtime:  req.Runtime,
				Trigger:  req.Trigger,
				Priority: req.Priority,
				Enabled:  req.Enabled,
				Module:   module,
			}); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
				return
			}
			def, _ := mgr.GetFunction(req.Name)
			writeJSONResponse(w, http.StatusCreated, map[string]any{"function": toFunctionResponse(def)})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func functionsHTTPInvokeHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handled := applyFunctionsHTTPCORS(w, r, opts); handled {
			return
		}

		mgr := opts.Functions
		if mgr == nil || !mgr.Enabled() {
			http.Error(w, `{"error":"functions runtime is disabled"}`, http.StatusServiceUnavailable)
			return
		}

		suffix := strings.TrimPrefix(r.URL.Path, "/fn/")
		suffix = strings.Trim(strings.TrimSpace(suffix), "/")
		parts := strings.Split(suffix, "/")
		name := strings.TrimSpace(parts[0])
		if name == "" {
			http.Error(w, `{"error":"function name is required"}`, http.StatusBadRequest)
			return
		}

		if _, err := mgr.GetFunction(name); err != nil {
			http.NotFound(w, r)
			return
		}

		path := "/"
		if len(parts) > 1 {
			path = "/" + strings.Join(parts[1:], "/")
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
			return
		}

		headers := make(map[string]string, len(r.Header))
		for k, values := range r.Header {
			headers[k] = strings.Join(values, ",")
		}

		event := map[string]any{
			"type":      functions.EventTypeHTTP,
			"phase":     "invoke",
			"method":    r.Method,
			"path":      path,
			"raw_path":  r.URL.Path,
			"query":     r.URL.Query(),
			"raw_query": r.URL.RawQuery,
			"headers":   headers,
			"body":      string(bodyBytes),
		}

		if len(bodyBytes) > 0 && strings.Contains(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json") {
			var bodyJSON any
			if err := json.Unmarshal(bodyBytes, &bodyJSON); err == nil {
				event["body_json"] = bodyJSON
			}
		}

		payload, err := json.Marshal(event)
		if err != nil {
			http.Error(w, `{"error":"failed to encode invoke payload"}`, http.StatusInternalServerError)
			return
		}

		result, err := mgr.InvokeFunction(r.Context(), name, payload)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		if !result.Continue {
			http.Error(w, `{"error":"request blocked by function"}`, http.StatusForbidden)
			return
		}

		writeFunctionHTTPResult(w, result)
	}
}

func applyFunctionsHTTPCORS(w http.ResponseWriter, r *http.Request, opts Options) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}

	allowedOrigin, ok := resolveFunctionsCORSOrigin(origin, opts)
	if r.Method == http.MethodOptions && strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")) != "" {
		w.Header().Add("Vary", "Origin")
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")
		if !ok {
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		setFunctionsCORSHeaders(w, allowedOrigin, opts)
		methods := strings.TrimSpace(opts.FunctionsHTTPCORSAllowMethods)
		if methods == "" {
			methods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
		}
		headers := strings.TrimSpace(opts.FunctionsHTTPCORSAllowHeaders)
		if headers == "" {
			headers = strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers"))
			if headers == "" {
				headers = "Content-Type, Authorization"
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", methods)
		w.Header().Set("Access-Control-Allow-Headers", headers)
		if opts.FunctionsHTTPCORSMaxAge > 0 {
			w.Header().Set("Access-Control-Max-Age", strconv.Itoa(opts.FunctionsHTTPCORSMaxAge))
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	if !ok {
		return false
	}
	w.Header().Add("Vary", "Origin")
	setFunctionsCORSHeaders(w, allowedOrigin, opts)
	return false
}

func setFunctionsCORSHeaders(w http.ResponseWriter, allowedOrigin string, opts Options) {
	w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
	if opts.FunctionsHTTPCORSAllowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if exposed := strings.TrimSpace(opts.FunctionsHTTPCORSExposeHeaders); exposed != "" {
		w.Header().Set("Access-Control-Expose-Headers", exposed)
	}
}

func resolveFunctionsCORSOrigin(origin string, opts Options) (string, bool) {
	allowed := strings.TrimSpace(opts.FunctionsHTTPCORSAllowOrigin)
	if allowed == "" {
		return "", false
	}
	if allowed == "*" {
		if opts.FunctionsHTTPCORSAllowCredentials {
			return origin, true
		}
		return "*", true
	}
	for _, candidate := range strings.Split(allowed, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), origin) {
			return origin, true
		}
	}
	return "", false
}

func writeFunctionHTTPResult(w http.ResponseWriter, result functions.InvocationResult) {
	if len(result.Output) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var fnResp functionHTTPResponse
	if err := json.Unmarshal(result.Output, &fnResp); err == nil {
		if fnResp.Status <= 0 {
			fnResp.Status = http.StatusOK
		}
		for k, v := range fnResp.Headers {
			if strings.TrimSpace(k) == "" {
				continue
			}
			w.Header().Set(k, v)
		}
		writeFunctionHTTPBody(w, fnResp.Status, fnResp.Body)
		return
	}

	if !json.Valid(result.Output) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.TrimSpace(result.Output))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Output)
}

func writeFunctionHTTPBody(w http.ResponseWriter, status int, body any) {
	if body == nil {
		w.WriteHeader(status)
		return
	}
	switch v := body.(type) {
	case string:
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(v))
	default:
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		enc, err := json.Marshal(v)
		if err != nil {
			http.Error(w, `{"error":"failed to encode function response body"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(enc)
	}
}

func functionsItemHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mgr := opts.Functions
		if mgr == nil || !mgr.Enabled() {
			http.Error(w, `{"error":"functions runtime is disabled"}`, http.StatusServiceUnavailable)
			return
		}
		suffix := strings.TrimPrefix(r.URL.Path, "/functions/")
		suffix = strings.TrimSpace(strings.Trim(suffix, "/"))
		parts := strings.Split(suffix, "/")
		name := strings.TrimSpace(parts[0])
		if name == "" {
			http.Error(w, `{"error":"function name is required"}`, http.StatusBadRequest)
			return
		}
		action := ""
		if len(parts) > 1 {
			action = strings.TrimSpace(parts[1])
		}
		if action == "versions" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			versions, err := mgr.ListFunctionVersions(name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{"versions": versions})
			return
		}
		if action == "activate" {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var req activateVersionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Version <= 0 {
				http.Error(w, `{"error":"valid version is required"}`, http.StatusBadRequest)
				return
			}
			if err := mgr.ActivateFunctionVersion(name, req.Version); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
				return
			}
			def, _ := mgr.GetFunction(name)
			writeJSONResponse(w, http.StatusOK, map[string]any{"function": toFunctionResponse(def)})
			return
		}
		if action == "invoke" {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var req invokeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
				return
			}
			if len(req.Payload) == 0 {
				req.Payload = json.RawMessage(`{}`)
			}
			result, err := mgr.InvokeFunction(r.Context(), name, req.Payload)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{"result": result})
			return
		}
		if action != "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			def, err := mgr.GetFunction(name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{"function": toFunctionResponse(def)})
		case http.MethodPut:
			var req functionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
				return
			}
			var module []byte
			if req.ModuleBase64 != "" {
				decoded, err := base64.StdEncoding.DecodeString(req.ModuleBase64)
				if err != nil {
					http.Error(w, `{"error":"invalid module_base64"}`, http.StatusBadRequest)
					return
				}
				module = decoded
			}
			if req.Runtime == "" {
				req.Runtime = functions.RuntimeWazero
			}
			if err := mgr.UpdateFunction(name, functions.Function{
				Name:     name,
				Runtime:  req.Runtime,
				Trigger:  req.Trigger,
				Priority: req.Priority,
				Enabled:  req.Enabled,
				Module:   module,
			}); err != nil {
				if errors.Is(err, functions.ErrNotImplemented) {
					http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusNotImplemented)
					return
				}
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
				return
			}
			def, _ := mgr.GetFunction(name)
			writeJSONResponse(w, http.StatusOK, map[string]any{"function": toFunctionResponse(def)})
		case http.MethodDelete:
			if err := mgr.DeleteFunction(name); err != nil {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func toFunctionResponse(def functions.Function) map[string]any {
	return map[string]any{
		"name":           def.Name,
		"runtime":        def.Runtime,
		"trigger":        def.Trigger,
		"priority":       def.Priority,
		"enabled":        def.Enabled,
		"version":        def.Version,
		"active_version": def.ActiveVersion,
		"size_bytes":     len(def.Module),
		"created_at":     def.CreatedAt,
		"updated_at":     def.UpdatedAt,
	}
}

func writeJSONResponse(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
