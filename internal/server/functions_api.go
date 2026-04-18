package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
