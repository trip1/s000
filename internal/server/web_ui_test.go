package server

import (
	"bytes"
	"context"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/functions"
	"ds9labs.com/s000/internal/metadata"
)

func TestWebUIRequiresLoginAndRendersPagesAfterSession(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandler(t)

	unauthReq := httptest.NewRequest(http.MethodGet, "/app", nil)
	unauthRR := httptest.NewRecorder()
	h.ServeHTTP(unauthRR, unauthReq)
	if unauthRR.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect for unauthenticated /app, got %d", unauthRR.Code)
	}

	cookie := loginAndGetSessionCookie(t, h)

	paths := []string{"/app", "/app/buckets", "/app/buckets/photos", "/app/buckets/photos/objects", "/app/buckets/photos/objects/album/hello.txt", "/app/uploads", "/app/settings", "/app/audit"}
	for _, p := range paths {
		p := p
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			req.AddCookie(cookie)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200 for %s, got %d", p, rr.Code)
			}
		})
	}

	accessReq := httptest.NewRequest(http.MethodGet, "/app/buckets", nil)
	accessReq.AddCookie(cookie)
	accessRR := httptest.NewRecorder()
	h.ServeHTTP(accessRR, accessReq)
	if accessRR.Code != http.StatusOK {
		t.Fatalf("expected accessibility check status 200, got %d", accessRR.Code)
	}
	body := accessRR.Body.String()
	if !strings.Contains(body, "for=\"new-bucket-name\"") {
		t.Fatalf("expected labeled new bucket input in buckets page, got %q", body)
	}

	objReq := httptest.NewRequest(http.MethodGet, "/app/buckets/photos/objects", nil)
	objReq.AddCookie(cookie)
	objRR := httptest.NewRecorder()
	h.ServeHTTP(objRR, objReq)
	if objRR.Code != http.StatusOK {
		t.Fatalf("expected object page status 200, got %d", objRR.Code)
	}
	if !strings.Contains(objRR.Body.String(), "aria-live=\"polite\"") || !strings.Contains(objRR.Body.String(), "id=\"upload-status\"") {
		t.Fatalf("expected async accessibility markers in object page, got %q", objRR.Body.String())
	}

	uploadsReq := httptest.NewRequest(http.MethodGet, "/app/uploads", nil)
	uploadsReq.AddCookie(cookie)
	uploadsRR := httptest.NewRecorder()
	h.ServeHTTP(uploadsRR, uploadsReq)
	if uploadsRR.Code != http.StatusOK {
		t.Fatalf("expected uploads page status 200, got %d", uploadsRR.Code)
	}
	uploadsBody := uploadsRR.Body.String()
	if !strings.Contains(uploadsBody, "&#9881; photos") || !strings.Contains(uploadsBody, "href=\"/app/buckets/photos\"") {
		t.Fatalf("expected bucket settings gear link on uploads page, got %q", uploadsBody)
	}
}

func TestWebUIActionsRequireCSRF(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandler(t)
	cookie := loginAndGetSessionCookie(t, h)

	req := httptest.NewRequest(http.MethodPost, "/app/actions/create-bucket", strings.NewReader("bucket=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected csrf failure status 403, got %d", rr.Code)
	}
}

func TestWebUIHTMXCSRFHeaderAccepted(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandler(t)
	cookie := loginAndGetSessionCookie(t, h)
	csrf := extractCSRFToken(t, h, cookie, "/app/buckets")

	form := url.Values{"bucket": {"from-htmx"}}
	req := httptest.NewRequest(http.MethodPost, "/app/actions/create-bucket", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect status for htmx create-bucket, got %d", rr.Code)
	}

	bucketsReq := httptest.NewRequest(http.MethodGet, "/app/partials/buckets", nil)
	bucketsReq.AddCookie(cookie)
	bucketsRR := httptest.NewRecorder()
	h.ServeHTTP(bucketsRR, bucketsReq)
	if bucketsRR.Code != http.StatusOK || !strings.Contains(bucketsRR.Body.String(), "from-htmx") {
		t.Fatalf("expected new bucket from htmx create, status=%d body=%q", bucketsRR.Code, bucketsRR.Body.String())
	}
}

func TestWebUICreateBucketUploadAndDeleteObject(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandler(t)
	cookie := loginAndGetSessionCookie(t, h)
	csrf := extractCSRFToken(t, h, cookie, "/app/buckets")

	create := url.Values{"_csrf": {csrf}, "bucket": {"work"}}
	createReq := httptest.NewRequest(http.MethodPost, "/app/actions/create-bucket", strings.NewReader(create.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq.AddCookie(cookie)
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusSeeOther {
		t.Fatalf("expected create bucket redirect, got %d", createRR.Code)
	}

	uploadBody, uploadCT := multipartUploadBody(t, map[string]string{"_csrf": csrf, "bucket": "work", "key": "hello.txt"}, "file", "hello.txt", "hello")
	uploadReq := httptest.NewRequest(http.MethodPost, "/app/actions/upload-object", uploadBody)
	uploadReq.Header.Set("Content-Type", uploadCT)
	uploadReq.AddCookie(cookie)
	uploadRR := httptest.NewRecorder()
	h.ServeHTTP(uploadRR, uploadReq)
	if uploadRR.Code != http.StatusSeeOther {
		t.Fatalf("expected upload redirect, got %d", uploadRR.Code)
	}

	objReq := httptest.NewRequest(http.MethodGet, "/app/partials/objects?bucket=work", nil)
	objReq.AddCookie(cookie)
	objRR := httptest.NewRecorder()
	h.ServeHTTP(objRR, objReq)
	if objRR.Code != http.StatusOK || !strings.Contains(objRR.Body.String(), "hello.txt") {
		t.Fatalf("expected uploaded object in partial, status=%d body=%q", objRR.Code, objRR.Body.String())
	}

	deleteForm := url.Values{"_csrf": {csrf}, "bucket": {"work"}, "key": {"hello.txt"}}
	deleteReq := httptest.NewRequest(http.MethodPost, "/app/actions/delete-object", strings.NewReader(deleteForm.Encode()))
	deleteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteReq.AddCookie(cookie)
	deleteRR := httptest.NewRecorder()
	h.ServeHTTP(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusSeeOther {
		t.Fatalf("expected delete redirect, got %d", deleteRR.Code)
	}
}

func TestWebUIBucketSettingsActions(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandler(t)
	cookie := loginAndGetSessionCookie(t, h)
	csrf := extractCSRFToken(t, h, cookie, "/app/buckets/photos")

	versioning := url.Values{"_csrf": {csrf}, "bucket": {"photos"}, "versioning": {"Enabled"}}
	versioningReq := httptest.NewRequest(http.MethodPost, "/app/actions/update-bucket-versioning", strings.NewReader(versioning.Encode()))
	versioningReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	versioningReq.AddCookie(cookie)
	versioningRR := httptest.NewRecorder()
	h.ServeHTTP(versioningRR, versioningReq)
	if versioningRR.Code != http.StatusSeeOther {
		t.Fatalf("expected versioning redirect, got %d", versioningRR.Code)
	}

	publicAccess := url.Values{
		"_csrf":                   {csrf},
		"bucket":                  {"photos"},
		"block_public_acls":       {"on"},
		"ignore_public_acls":      {"on"},
		"block_public_policy":     {"on"},
		"restrict_public_buckets": {"on"},
	}
	publicReq := httptest.NewRequest(http.MethodPost, "/app/actions/update-bucket-public-access", strings.NewReader(publicAccess.Encode()))
	publicReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	publicReq.AddCookie(cookie)
	publicRR := httptest.NewRecorder()
	h.ServeHTTP(publicRR, publicReq)
	if publicRR.Code != http.StatusSeeOther {
		t.Fatalf("expected public access redirect, got %d", publicRR.Code)
	}

	policy := url.Values{"_csrf": {csrf}, "bucket": {"photos"}, "policy_enabled": {"on"}, "policy_document": {`{"Version":"2012-10-17","Statement":[]}`}}
	policyReq := httptest.NewRequest(http.MethodPost, "/app/actions/update-bucket-policy", strings.NewReader(policy.Encode()))
	policyReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	policyReq.AddCookie(cookie)
	policyRR := httptest.NewRecorder()
	h.ServeHTTP(policyRR, policyReq)
	if policyRR.Code != http.StatusSeeOther {
		t.Fatalf("expected policy redirect, got %d", policyRR.Code)
	}

	cors := url.Values{"_csrf": {csrf}, "bucket": {"photos"}, "cors_enabled": {"on"}, "allowed_origins": {"https://example.com"}, "allowed_methods": {"GET,PUT"}, "max_age_seconds": {"3600"}}
	corsReq := httptest.NewRequest(http.MethodPost, "/app/actions/update-bucket-cors", strings.NewReader(cors.Encode()))
	corsReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	corsReq.AddCookie(cookie)
	corsRR := httptest.NewRecorder()
	h.ServeHTTP(corsRR, corsReq)
	if corsRR.Code != http.StatusSeeOther {
		t.Fatalf("expected cors redirect, got %d", corsRR.Code)
	}

	website := url.Values{"_csrf": {csrf}, "bucket": {"photos"}, "website_enabled": {"on"}, "website_public_read": {"on"}, "index_document": {"index.html"}, "error_document": {"error.html"}}
	websiteReq := httptest.NewRequest(http.MethodPost, "/app/actions/update-bucket-website", strings.NewReader(website.Encode()))
	websiteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	websiteReq.AddCookie(cookie)
	websiteRR := httptest.NewRecorder()
	h.ServeHTTP(websiteRR, websiteReq)
	if websiteRR.Code != http.StatusSeeOther {
		t.Fatalf("expected website redirect, got %d", websiteRR.Code)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/app/buckets/photos", nil)
	detailReq.AddCookie(cookie)
	detailRR := httptest.NewRecorder()
	h.ServeHTTP(detailRR, detailReq)
	if detailRR.Code != http.StatusOK {
		t.Fatalf("expected bucket detail status 200, got %d", detailRR.Code)
	}
	body := detailRR.Body.String()
	checks := []string{
		`action="/app/actions/update-bucket-versioning"`,
		`action="/app/actions/update-bucket-public-access"`,
		`action="/app/actions/update-bucket-website"`,
		`action="/app/actions/update-bucket-cors"`,
		`action="/app/actions/update-bucket-policy"`,
		`value="Enabled" selected`,
		`name="policy_document"`,
		`https://example.com`,
		`index.html`,
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("expected bucket detail to contain %q, body=%q", check, body)
		}
	}
}

func TestWebUIPartialsAndAssets(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandler(t)
	cookie := loginAndGetSessionCookie(t, h)
	partials := []string{"/app/partials/buckets", "/app/partials/objects?bucket=photos", "/app/partials/object-metadata?bucket=photos&key=album/hello.txt", "/app/partials/flash", "/app/partials/pagination?bucket=photos"}
	for _, p := range partials {
		p := p
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			req.AddCookie(cookie)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}
		})
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	assetRR := httptest.NewRecorder()
	h.ServeHTTP(assetRR, assetReq)
	if assetRR.Code != http.StatusOK {
		t.Fatalf("expected static asset status 200, got %d", assetRR.Code)
	}
	if !strings.Contains(assetRR.Body.String(), "@media (max-width: 768px)") {
		t.Fatalf("expected responsive media query in css, got %q", assetRR.Body.String())
	}
	if !strings.Contains(assetRR.Body.String(), "html[data-theme=\"sysadmin90\"]") {
		t.Fatalf("expected themed css variables, got %q", assetRR.Body.String())
	}

	jsReq := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	jsRR := httptest.NewRecorder()
	h.ServeHTTP(jsRR, jsReq)
	if jsRR.Code != http.StatusOK {
		t.Fatalf("expected app.js status 200, got %d", jsRR.Code)
	}
	if !strings.Contains(jsRR.Body.String(), "Uploading") {
		t.Fatalf("expected upload progress JS, got %q", jsRR.Body.String())
	}
}

func TestWebUIThemeFromConfigAndThemeAction(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandlerWithTheme(t, "sysadmin90")
	cookie := loginAndGetSessionCookie(t, h)

	settingsReq := httptest.NewRequest(http.MethodGet, "/app/settings", nil)
	settingsReq.AddCookie(cookie)
	settingsRR := httptest.NewRecorder()
	h.ServeHTTP(settingsRR, settingsReq)
	if settingsRR.Code != http.StatusOK {
		t.Fatalf("expected settings status 200, got %d", settingsRR.Code)
	}
	if !strings.Contains(settingsRR.Body.String(), "data-theme=\"sysadmin90\"") {
		t.Fatalf("expected configured theme sysadmin90 in settings page, got %q", settingsRR.Body.String())
	}
}

func TestWebUIFunctionsPagesPartialsAndActions(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandlerWithFunctions(t)
	cookie := loginAndGetSessionCookie(t, h)
	csrf := extractCSRFToken(t, h, cookie, "/app/functions")

	pageReq := httptest.NewRequest(http.MethodGet, "/app/functions", nil)
	pageReq.AddCookie(cookie)
	pageRR := httptest.NewRecorder()
	h.ServeHTTP(pageRR, pageReq)
	if pageRR.Code != http.StatusOK {
		t.Fatalf("expected functions page status 200, got %d", pageRR.Code)
	}
	if !strings.Contains(pageRR.Body.String(), "Functions") {
		t.Fatalf("expected functions page content, got %q", pageRR.Body.String())
	}
	pageBody := pageRR.Body.String()
	if !strings.Contains(pageBody, "Configuration Inspector") || !strings.Contains(pageBody, "aria-live=\"polite\"") {
		t.Fatalf("expected configuration inspector and aria-live markers in functions page, got %q", pageBody)
	}
	if !strings.Contains(pageBody, "HTTP gateway public") || !strings.Contains(pageBody, "https://app.example.com") || !strings.Contains(pageBody, "CORS allow credentials") {
		t.Fatalf("expected function HTTP gateway and CORS config in inspector, got %q", pageBody)
	}

	createForm := url.Values{
		"_csrf":         {csrf},
		"name":          {"ui-fn"},
		"runtime":       {"wazero"},
		"trigger":       {"onPutObjectPre"},
		"priority":      {"100"},
		"enabled":       {"on"},
		"module_base64": {"bW9kdWxl"},
	}
	createReq := httptest.NewRequest(http.MethodPost, "/app/actions/functions/create", strings.NewReader(createForm.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq.AddCookie(cookie)
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusSeeOther {
		t.Fatalf("expected functions create redirect, got %d body=%q", createRR.Code, createRR.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/app/partials/functions", nil)
	listReq.AddCookie(cookie)
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK || !strings.Contains(listRR.Body.String(), "ui-fn") {
		t.Fatalf("expected functions partial to include ui-fn, status=%d body=%q", listRR.Code, listRR.Body.String())
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/app/functions/ui-fn", nil)
	detailReq.AddCookie(cookie)
	detailRR := httptest.NewRecorder()
	h.ServeHTTP(detailRR, detailReq)
	if detailRR.Code != http.StatusOK {
		t.Fatalf("expected function detail status 200, got %d", detailRR.Code)
	}
	detailBody := detailRR.Body.String()
	if !strings.Contains(detailBody, "Delete this function?") {
		t.Fatalf("expected delete confirmation guardrail in function detail page, got %q", detailBody)
	}

	invokeForm := url.Values{"_csrf": {csrf}, "name": {"ui-fn"}, "payload": {`{"bucket":"photos"}`}}
	invokeReq := httptest.NewRequest(http.MethodPost, "/app/actions/functions/invoke", strings.NewReader(invokeForm.Encode()))
	invokeReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	invokeReq.AddCookie(cookie)
	invokeRR := httptest.NewRecorder()
	h.ServeHTTP(invokeRR, invokeReq)
	if invokeRR.Code != http.StatusSeeOther {
		t.Fatalf("expected invoke redirect, got %d body=%q", invokeRR.Code, invokeRR.Body.String())
	}

	versionsReq := httptest.NewRequest(http.MethodGet, "/app/partials/function-versions?name=ui-fn", nil)
	versionsReq.AddCookie(cookie)
	versionsRR := httptest.NewRecorder()
	h.ServeHTTP(versionsRR, versionsReq)
	if versionsRR.Code != http.StatusOK {
		t.Fatalf("expected versions partial status 200, got %d", versionsRR.Code)
	}
	if !strings.Contains(versionsRR.Body.String(), "Activate") {
		t.Fatalf("expected activate control in versions partial, got %q", versionsRR.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/app/partials/function-metrics", nil)
	metricsReq.AddCookie(cookie)
	metricsRR := httptest.NewRecorder()
	h.ServeHTTP(metricsRR, metricsReq)
	if metricsRR.Code != http.StatusOK {
		t.Fatalf("expected metrics partial status 200, got %d", metricsRR.Code)
	}

	alertsReq := httptest.NewRequest(http.MethodGet, "/app/partials/function-alerts", nil)
	alertsReq.AddCookie(cookie)
	alertsRR := httptest.NewRecorder()
	h.ServeHTTP(alertsRR, alertsReq)
	if alertsRR.Code != http.StatusOK {
		t.Fatalf("expected alerts partial status 200, got %d", alertsRR.Code)
	}

	logsReq := httptest.NewRequest(http.MethodGet, "/app/partials/function-logs?limit=10", nil)
	logsReq.AddCookie(cookie)
	logsRR := httptest.NewRecorder()
	h.ServeHTTP(logsRR, logsReq)
	if logsRR.Code != http.StatusOK {
		t.Fatalf("expected logs partial status 200, got %d", logsRR.Code)
	}

	deleteForm := url.Values{"_csrf": {csrf}, "name": {"ui-fn"}}
	deleteReq := httptest.NewRequest(http.MethodPost, "/app/actions/functions/delete", strings.NewReader(deleteForm.Encode()))
	deleteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteReq.AddCookie(cookie)
	deleteRR := httptest.NewRecorder()
	h.ServeHTTP(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusSeeOther {
		t.Fatalf("expected delete redirect, got %d", deleteRR.Code)
	}
}

func TestWebUIFunctionsWasmFileUploadCreateAndUpdate(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandlerWithFunctions(t)
	cookie := loginAndGetSessionCookie(t, h)
	csrf := extractCSRFToken(t, h, cookie, "/app/functions")

	createBody, createType := multipartUploadBody(t, map[string]string{
		"_csrf":    csrf,
		"name":     "ui-fn-file",
		"runtime":  "wazero",
		"trigger":  "onHTTPPre",
		"priority": "100",
		"enabled":  "on",
	}, "module_file", "fn.wasm", "module-v1")
	createReq := httptest.NewRequest(http.MethodPost, "/app/actions/functions/create", createBody)
	createReq.Header.Set("Content-Type", createType)
	createReq.AddCookie(cookie)
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusSeeOther {
		t.Fatalf("expected create redirect for wasm upload, got %d body=%q", createRR.Code, createRR.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/app/partials/functions", nil)
	listReq.AddCookie(cookie)
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK || !strings.Contains(listRR.Body.String(), "ui-fn-file") {
		t.Fatalf("expected functions partial to include uploaded function, status=%d body=%q", listRR.Code, listRR.Body.String())
	}

	updateBody, updateType := multipartUploadBody(t, map[string]string{
		"_csrf":    csrf,
		"name":     "ui-fn-file",
		"runtime":  "wazero",
		"trigger":  "onHTTPPost",
		"priority": "120",
		"enabled":  "on",
	}, "module_file", "fn-v2.wasm", "module-v2")
	updateReq := httptest.NewRequest(http.MethodPost, "/app/actions/functions/update", updateBody)
	updateReq.Header.Set("Content-Type", updateType)
	updateReq.AddCookie(cookie)
	updateRR := httptest.NewRecorder()
	h.ServeHTTP(updateRR, updateReq)
	if updateRR.Code != http.StatusSeeOther {
		t.Fatalf("expected update redirect for wasm upload, got %d body=%q", updateRR.Code, updateRR.Body.String())
	}

	versionsReq := httptest.NewRequest(http.MethodGet, "/app/partials/function-versions?name=ui-fn-file", nil)
	versionsReq.AddCookie(cookie)
	versionsRR := httptest.NewRecorder()
	h.ServeHTTP(versionsRR, versionsReq)
	if versionsRR.Code != http.StatusOK {
		t.Fatalf("expected versions partial status 200, got %d", versionsRR.Code)
	}
	if !strings.Contains(versionsRR.Body.String(), "value=\"2\"") {
		t.Fatalf("expected version 2 entry after wasm file update, got %q", versionsRR.Body.String())
	}
}

func TestWebUIFunctionsActionsRequireCSRF(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandlerWithFunctions(t)
	cookie := loginAndGetSessionCookie(t, h)

	req := httptest.NewRequest(http.MethodPost, "/app/actions/functions/create", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected csrf failure status 403, got %d", rr.Code)
	}
}

func TestWebUIFunctionsRoutesRequireLogin(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandlerWithFunctions(t)
	paths := []string{"/app/functions", "/app/functions/sample", "/app/partials/functions", "/app/partials/function-metrics", "/app/partials/function-alerts", "/app/partials/function-logs"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("expected redirect status 303 for %s, got %d", p, rr.Code)
		}
	}
}

func TestWebUIFunctionsHTMXOptimisticRefresh(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandlerWithFunctions(t)
	cookie := loginAndGetSessionCookie(t, h)
	csrf := extractCSRFToken(t, h, cookie, "/app/functions")

	form := url.Values{
		"_csrf":         {csrf},
		"name":          {"htmx-fn"},
		"runtime":       {"wazero"},
		"trigger":       {"onPutObjectPre"},
		"priority":      {"100"},
		"enabled":       {"on"},
		"module_base64": {"bW9kdWxl"},
	}
	req := httptest.NewRequest(http.MethodPost, "/app/actions/functions/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected htmx status 200, got %d", rr.Code)
	}
	if trigger := rr.Header().Get("HX-Trigger"); trigger != "functions-changed" {
		t.Fatalf("expected HX-Trigger functions-changed, got %q", trigger)
	}
	if !strings.Contains(rr.Body.String(), "function created") {
		t.Fatalf("expected action flash in body, got %q", rr.Body.String())
	}
}

func TestWebUIObjectDownloadAction(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandler(t)
	cookie := loginAndGetSessionCookie(t, h)

	req := httptest.NewRequest(http.MethodGet, "/app/actions/download-object?bucket=photos&key=album/hello.txt", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected download status 200, got %d", rr.Code)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("expected attachment content disposition, got %q", cd)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("expected fallback content type application/octet-stream, got %q", ct)
	}
	if body := rr.Body.String(); body != "hello" {
		t.Fatalf("expected downloaded body hello, got %q", body)
	}
}

func TestWebUISpecialCharacterKeysEncodedLinksAndActions(t *testing.T) {
	t.Parallel()

	h := newAuthedWebUIHandler(t)
	cookie := loginAndGetSessionCookie(t, h)

	specialKey := "folder name/uni+code #1?.txt"

	objectsReq := httptest.NewRequest(http.MethodGet, "/app/partials/objects?bucket=photos", nil)
	objectsReq.AddCookie(cookie)
	objectsRR := httptest.NewRecorder()
	h.ServeHTTP(objectsRR, objectsReq)
	if objectsRR.Code != http.StatusOK {
		t.Fatalf("expected objects partial 200, got %d", objectsRR.Code)
	}
	expectedPath := "/app/buckets/photos/objects/" + url.PathEscape(specialKey)
	decodedBody := html.UnescapeString(objectsRR.Body.String())
	if !strings.Contains(decodedBody, expectedPath) {
		t.Fatalf("expected encoded object link %q in body %q", expectedPath, decodedBody)
	}

	metaReq := httptest.NewRequest(http.MethodGet, "/app/partials/object-metadata?bucket=photos&key="+url.QueryEscape(specialKey), nil)
	metaReq.AddCookie(cookie)
	metaRR := httptest.NewRecorder()
	h.ServeHTTP(metaRR, metaReq)
	if metaRR.Code != http.StatusOK {
		t.Fatalf("expected metadata partial 200, got %d", metaRR.Code)
	}
	metaBody := html.UnescapeString(metaRR.Body.String())
	if !strings.Contains(metaBody, specialKey) {
		t.Fatalf("expected metadata panel to include special key, got %q", metaBody)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/app/actions/download-object?bucket=photos&key="+url.QueryEscape(specialKey), nil)
	downloadReq.AddCookie(cookie)
	downloadRR := httptest.NewRecorder()
	h.ServeHTTP(downloadRR, downloadReq)
	if downloadRR.Code != http.StatusOK {
		t.Fatalf("expected special-key download status 200, got %d", downloadRR.Code)
	}
	if cd := downloadRR.Header().Get("Content-Disposition"); !strings.Contains(cd, "filename*=UTF-8''") {
		t.Fatalf("expected RFC5987 filename* in content disposition, got %q", cd)
	}
	if ct := downloadRR.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("expected stored content type for special object, got %q", ct)
	}
	if body := downloadRR.Body.String(); body != "special" {
		t.Fatalf("expected downloaded special object body, got %q", body)
	}
}

func newAuthedWebUIHandler(t *testing.T) http.Handler {
	return newAuthedWebUIHandlerWithTheme(t, "sysadmin90")
}

func newAuthedWebUIHandlerWithFunctions(t *testing.T) http.Handler {
	t.Helper()

	ctx := context.Background()
	root := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	if err := mstore.CreateBucket(ctx, metadata.Bucket{Name: "photos", CreatedAt: time.Now().UTC(), VersioningStatus: "Suspended", Region: "us-east-1"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	mgr, err := functions.NewManager(functions.Config{Enabled: true, Dir: t.TempDir(), Runtime: functions.RuntimeWazero, MemoryLimitMB: 64, CPULimit: 100 * time.Millisecond, ReloadInterval: 2 * time.Second})
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	mgr.SetRuntimeForTesting(fakeRuntime{output: []byte(`{"continue":true,"output":{"ok":true}}`)})

	return NewHandler(Options{
		Metadata:                          mstore,
		Blob:                              bstore,
		UIAccessKey:                       "admin",
		UISecretKey:                       "secret",
		UITheme:                           "sysadmin90",
		Functions:                         mgr,
		FunctionsHTTPPublic:               true,
		FunctionsHTTPCORSAllowOrigin:      "https://app.example.com",
		FunctionsHTTPCORSAllowMethods:     "GET,POST,OPTIONS",
		FunctionsHTTPCORSAllowHeaders:     "Content-Type,Authorization",
		FunctionsHTTPCORSExposeHeaders:    "X-Trace-Id",
		FunctionsHTTPCORSMaxAge:           1200,
		FunctionsHTTPCORSAllowCredentials: true,
	})
}

func newAuthedWebUIHandlerWithTheme(t *testing.T, theme string) http.Handler {
	t.Helper()

	ctx := context.Background()
	root := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	if err := mstore.CreateBucket(ctx, metadata.Bucket{Name: "photos", CreatedAt: now, VersioningStatus: "Suspended", Region: "us-east-1"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	meta, err := bstore.WriteObject(ctx, blob.ObjectRef{Bucket: "photos", Key: "album/hello.txt", VersionID: "null"}, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("write blob failed: %v", err)
	}
	if err := mstore.PutObjectVersion(ctx, metadata.ObjectVersion{Bucket: "photos", Key: "album/hello.txt", VersionID: "null", Size: meta.Size, ETag: meta.MD5Hex, ChecksumSHA256: meta.SHA256, StoragePath: meta.Path, CreatedAt: now}); err != nil {
		t.Fatalf("put object failed: %v", err)
	}
	specialKey := "folder name/uni+code #1?.txt"
	specialMeta, err := bstore.WriteObject(ctx, blob.ObjectRef{Bucket: "photos", Key: specialKey, VersionID: "null"}, strings.NewReader("special"))
	if err != nil {
		t.Fatalf("write special blob failed: %v", err)
	}
	if err := mstore.PutObjectVersion(ctx, metadata.ObjectVersion{Bucket: "photos", Key: specialKey, VersionID: "null", Size: specialMeta.Size, ETag: specialMeta.MD5Hex, ChecksumSHA256: specialMeta.SHA256, StoragePath: specialMeta.Path, Metadata: map[string]string{"content-type": "text/plain; charset=utf-8"}, CreatedAt: now}); err != nil {
		t.Fatalf("put special object failed: %v", err)
	}

	return NewHandler(Options{Metadata: mstore, Blob: bstore, UIAccessKey: "admin", UISecretKey: "secret", UITheme: theme})
}

func loginAndGetSessionCookie(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()

	body := strings.NewReader("access_key=admin&secret_key=secret")
	req := httptest.NewRequest(http.MethodPost, "/app/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected login redirect, got %d body=%q", rr.Code, rr.Body.String())
	}
	resp := rr.Result()
	defer func() { _ = resp.Body.Close() }()
	for _, c := range resp.Cookies() {
		if c.Name == uiSessionCookie {
			return c
		}
	}
	t.Fatal("expected session cookie")
	return nil
}

func extractCSRFToken(t *testing.T, h http.Handler, cookie *http.Cookie, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected page status 200, got %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Result().Body)
	text := string(body)
	needle := "name=\"_csrf\" value=\""
	i := strings.Index(text, needle)
	if i < 0 {
		t.Fatalf("csrf token not found in page: %s", text)
	}
	start := i + len(needle)
	end := strings.Index(text[start:], "\"")
	if end < 0 {
		t.Fatalf("csrf token end not found in page: %s", text)
	}
	return text[start : start+end]
}

func multipartUploadBody(t *testing.T, fields map[string]string, fileField string, fileName string, fileContent string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s failed: %v", k, err)
		}
	}
	filePart, err := w.CreateFormFile(fileField, fileName)
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := io.Copy(filePart, strings.NewReader(fileContent)); err != nil {
		t.Fatalf("write file content failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer failed: %v", err)
	}
	return &body, w.FormDataContentType()
}
