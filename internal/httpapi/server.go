package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/apperr"
	"github.com/11DingKing/silvercare-coordination/internal/audit"
	"github.com/11DingKing/silvercare-coordination/internal/auth"
	"github.com/11DingKing/silvercare-coordination/internal/benefit"
	"github.com/11DingKing/silvercare-coordination/internal/eligibility"
	"github.com/11DingKing/silvercare-coordination/internal/escalation"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/plan"
	"github.com/11DingKing/silvercare-coordination/internal/provider"
	"github.com/11DingKing/silvercare-coordination/internal/resident"
	"github.com/11DingKing/silvercare-coordination/internal/resource"
	"github.com/11DingKing/silvercare-coordination/internal/visit"
)

type Readiness interface{ Ping(context.Context) error }

type Services struct {
	Auth        *auth.Service
	Residents   *resident.Service
	Eligibility *eligibility.Service
	Plans       *plan.Service
	Providers   *provider.Service
	Benefits    *benefit.Service
	Visits      *visit.Service
	Resources   *resource.Service
	Escalations *escalation.Service
	Audit       *audit.Service
}

type API struct {
	services  Services
	readiness Readiness
	ids       idgen.Generator
	logger    *slog.Logger
	handler   http.Handler
}

type contextKey string

const principalKey contextKey = "principal"

func New(services Services, readiness Readiness, ids idgen.Generator, logger *slog.Logger) *API {
	api := &API{services: services, readiness: readiness, ids: ids, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", api.live)
	mux.HandleFunc("GET /health/ready", api.ready)
	mux.HandleFunc("POST /v1/sessions", api.login)
	mux.Handle("DELETE /v1/sessions/current", api.requireAuth(http.HandlerFunc(api.logout)))
	mux.Handle("GET /v1/me", api.requireAuth(http.HandlerFunc(api.me)))
	mux.Handle("POST /v1/residents", api.requireAuth(http.HandlerFunc(api.createResident)))
	mux.Handle("GET /v1/residents", api.requireAuth(http.HandlerFunc(api.listResidents)))
	mux.Handle("GET /v1/residents/{id}", api.requireAuth(http.HandlerFunc(api.getResident)))
	mux.Handle("POST /v1/residents/{id}/consent", api.requireAuth(http.HandlerFunc(api.grantConsent)))
	mux.Handle("DELETE /v1/residents/{id}/consent", api.requireAuth(http.HandlerFunc(api.withdrawConsent)))
	mux.Handle("POST /v1/residents/{id}/assessments", api.requireAuth(http.HandlerFunc(api.startAssessment)))
	mux.Handle("POST /v1/assessments/{id}/evidence", api.requireAuth(http.HandlerFunc(api.addAssessmentEvidence)))
	mux.Handle("POST /v1/assessments/{id}/submit", api.requireAuth(http.HandlerFunc(api.submitAssessment)))
	mux.Handle("POST /v1/assessments/{id}/approve", api.requireAuth(http.HandlerFunc(api.approveAssessment)))
	mux.Handle("POST /v1/residents/{id}/plans", api.requireAuth(http.HandlerFunc(api.createPlan)))
	mux.Handle("POST /v1/plans/{id}/goals", api.requireAuth(http.HandlerFunc(api.addPlanGoal)))
	mux.Handle("POST /v1/plans/{id}/submit", api.requireAuth(http.HandlerFunc(api.submitPlan)))
	mux.Handle("POST /v1/plans/{id}/activate", api.requireAuth(http.HandlerFunc(api.activatePlan)))
	mux.Handle("POST /v1/providers", api.requireAuth(http.HandlerFunc(api.registerProvider)))
	mux.Handle("POST /v1/providers/{id}/activate", api.requireAuth(http.HandlerFunc(api.activateProvider)))
	mux.Handle("POST /v1/plans/{id}/authorizations", api.requireAuth(http.HandlerFunc(api.createAuthorization)))
	mux.Handle("POST /v1/authorizations/{id}/activate", api.requireAuth(http.HandlerFunc(api.activateAuthorization)))
	mux.Handle("POST /v1/visits", api.requireAuth(http.HandlerFunc(api.scheduleVisit)))
	mux.Handle("POST /v1/visits/{id}/assign", api.requireAuth(http.HandlerFunc(api.assignVisit)))
	mux.Handle("POST /v1/visits/{id}/accept", api.requireAuth(http.HandlerFunc(api.acceptVisit)))
	mux.Handle("POST /v1/visits/{id}/travel", api.requireAuth(http.HandlerFunc(api.startVisitTravel)))
	mux.Handle("POST /v1/visits/{id}/check-in", api.requireAuth(http.HandlerFunc(api.checkInVisit)))
	mux.Handle("POST /v1/visits/{id}/complete", api.requireAuth(http.HandlerFunc(api.completeVisit)))
	mux.Handle("POST /v1/visits/{id}/claims", api.requireAuth(http.HandlerFunc(api.createClaim)))
	mux.Handle("POST /v1/claims/{id}/review", api.requireAuth(http.HandlerFunc(api.reviewClaim)))
	mux.Handle("POST /v1/resources", api.requireAuth(http.HandlerFunc(api.registerResource)))
	mux.Handle("POST /v1/resources/{id}/assign", api.requireAuth(http.HandlerFunc(api.assignResource)))
	mux.Handle("POST /v1/resources/{id}/return", api.requireAuth(http.HandlerFunc(api.returnResource)))
	mux.Handle("POST /v1/escalations", api.requireAuth(http.HandlerFunc(api.openEscalation)))
	mux.Handle("POST /v1/escalations/{id}/acknowledge", api.requireAuth(http.HandlerFunc(api.acknowledgeEscalation)))
	mux.Handle("POST /v1/escalations/{id}/resolve", api.requireAuth(http.HandlerFunc(api.resolveEscalation)))
	mux.Handle("GET /v1/audit/{type}/{id}", api.requireAuth(http.HandlerFunc(api.listAudit)))
	api.handler = api.recoverPanic(api.requestMetadata(mux))
	return api
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }

func (a *API) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.readiness.Ping(ctx); err != nil {
		a.writeError(w, r, apperr.Wrap(apperr.CodeUnavailable, "readiness", "database is unavailable", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (a *API) requestMetadata(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID, _ = a.ids.New("request")
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), contextKey("request_id"), requestID)
		started := time.Now()
		next.ServeHTTP(w, r.WithContext(ctx))
		a.logger.InfoContext(ctx, "http request", "method", r.Method, "path", r.URL.Path,
			"request_id", requestID, "duration_ms", time.Since(started).Milliseconds())
	})
}

func (a *API) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.ErrorContext(r.Context(), "http panic recovered", "panic", recovered, "stack", string(debug.Stack()))
				a.writeError(w, r, apperr.New(apperr.CodeInternal, "the request could not be completed"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *API) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			a.writeError(w, r, apperr.New(apperr.CodeUnauthorized, "bearer session is required"))
			return
		}
		principal, err := a.services.Auth.Authenticate(r.Context(), parts[1], requestID(r))
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, principal)))
	})
}

func requestID(r *http.Request) string {
	value, _ := r.Context().Value(contextKey("request_id")).(string)
	return value
}
func principal(r *http.Request) auth.Principal {
	value, _ := r.Context().Value(principalKey).(auth.Principal)
	return value
}

type errorResponse struct {
	Error errorBody `json:"error"`
}
type errorBody struct {
	Code      apperr.Code       `json:"code"`
	Message   string            `json:"message"`
	RequestID string            `json:"request_id"`
	Details   map[string]string `json:"details,omitempty"`
}

func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	body := errorBody{Code: apperr.CodeInternal, Message: "the request could not be completed", RequestID: requestID(r)}
	if app, ok := apperr.As(err); ok {
		body.Code, body.Message, body.Details = app.Code, app.Message, app.Details
		switch app.Code {
		case apperr.CodeValidation:
			status = http.StatusBadRequest
		case apperr.CodeUnauthorized:
			status = http.StatusUnauthorized
		case apperr.CodeForbidden:
			status = http.StatusForbidden
		case apperr.CodeNotFound:
			status = http.StatusNotFound
		case apperr.CodeConflict, apperr.CodeExpired:
			status = http.StatusConflict
		case apperr.CodeUnavailable:
			status = http.StatusServiceUnavailable
		}
	}
	if status >= 500 {
		a.logger.ErrorContext(r.Context(), "http request failed", "error", err, "request_id", body.RequestID)
	}
	writeJSON(w, status, errorResponse{Error: body})
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return apperr.Validation("body", err.Error())
	}
	if decoder.Decode(&struct{}{}) == nil {
		return apperr.Validation("body", "only one JSON object is allowed")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func parseTime(value, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, apperr.Validation(field, "must be RFC3339")
	}
	return parsed, nil
}

func requireVersion(value int64) error {
	if value <= 0 {
		return apperr.Validation("expected_version", "must be positive")
	}
	return nil
}

func (a *API) bind(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := decodeJSON(r, target); err != nil {
		a.writeError(w, r, err)
		return false
	}
	return true
}

func (a *API) result(w http.ResponseWriter, status int, value any) {
	writeJSON(w, status, map[string]any{"data": value})
}

var _ = errors.Is
var _ = fmt.Sprintf
