package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/11DingKing/silvercare-coordination/internal/idgen"
)

type readinessStub struct{ err error }

func (r readinessStub) Ping(context.Context) error { return r.err }

func testAPI(ready Readiness) *API {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Services{}, ready, &idgen.Sequence{}, logger)
}

func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return body
}

func TestLivenessDoesNotDependOnDatabase(t *testing.T) {
	api := testAPI(readinessStub{err: errors.New("database offline")})
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := decodeResponse(t, recorder)
	if body["status"] != "alive" {
		t.Fatalf("body = %#v", body)
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("request id header missing")
	}
}

func TestReadinessReportsDependencyFailure(t *testing.T) {
	api := testAPI(readinessStub{err: errors.New("database offline")})
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set("X-Request-ID", "request-client")
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("X-Request-ID") != "request-client" {
		t.Fatalf("request id = %q", recorder.Header().Get("X-Request-ID"))
	}
	body := decodeResponse(t, recorder)
	errorObject, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("body = %#v", body)
	}
	if errorObject["code"] != "unavailable" || errorObject["request_id"] != "request-client" {
		t.Fatalf("error = %#v", errorObject)
	}
}

func TestReadinessReturnsReadyWhenDatabaseResponds(t *testing.T) {
	api := testAPI(readinessStub{})
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if decodeResponse(t, recorder)["status"] != "ready" {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestProtectedRouteRequiresBearerSession(t *testing.T) {
	api := testAPI(readinessStub{})
	tests := []struct{ name, authorization string }{
		{"missing", ""},
		{"wrong scheme", "Basic abc"},
		{"missing token", "Bearer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			request.Header.Set("Authorization", test.authorization)
			recorder := httptest.NewRecorder()
			api.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			body := decodeResponse(t, recorder)
			errorObject := body["error"].(map[string]any)
			if errorObject["code"] != "unauthorized" {
				t.Fatalf("error = %#v", errorObject)
			}
		})
	}
}

func TestJSONDecoderRejectsUnknownAndMultipleObjects(t *testing.T) {
	tests := []struct{ name, payload string }{
		{"unknown field", `{"district_id":"d","email":"a@example.test","password":"long password","extra":true}`},
		{"two objects", `{"district_id":"d","email":"a@example.test","password":"long password"}{}`},
		{"malformed", `{"district_id":`},
	}
	api := testAPI(readinessStub{})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(test.payload))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			api.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestMethodMismatchDoesNotInvokeHandler(t *testing.T) {
	api := testAPI(readinessStub{})
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/health/live", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", recorder.Code)
	}
	if allow := recorder.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow = %q", allow)
	}
}
