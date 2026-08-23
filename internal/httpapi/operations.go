package httpapi

import (
	"net/http"
	"strconv"

	"github.com/11DingKing/silvercare-coordination/internal/apperr"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

func (a *API) scheduleVisit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AuthorizationID string `json:"authorization_id"`
		ProviderID      string `json:"provider_id"`
		ResidentID      string `json:"resident_id"`
		StartsAt        string `json:"starts_at"`
		EndsAt          string `json:"ends_at"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	startsAt, err := parseTime(body.StartsAt, "starts_at")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	endsAt, err := parseTime(body.EndsAt, "ends_at")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.services.Visits.Schedule(r.Context(), principal(r).Actor, body.AuthorizationID, body.ProviderID, body.ResidentID, startsAt, endsAt)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) assignVisit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID          string `json:"user_id"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Visits.Assign(r.Context(), principal(r).Actor, r.PathValue("id"), body.UserID, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) acceptVisit(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Visits.Accept(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}
func (a *API) startVisitTravel(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Visits.StartTravel(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) checkInVisit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		At              string `json:"at"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	at, err := parseTime(body.At, "at")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.services.Visits.CheckIn(r.Context(), principal(r).Actor, r.PathValue("id"), at, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) completeVisit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		At              string            `json:"at"`
		Evidence        map[string]string `json:"evidence"`
		ExpectedVersion int64             `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	at, err := parseTime(body.At, "at")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.services.Visits.Complete(r.Context(), principal(r).Actor, r.PathValue("id"), body.Evidence, at, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) createClaim(w http.ResponseWriter, r *http.Request) {
	result, err := a.services.Benefits.CreateClaim(r.Context(), principal(r).Actor, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) reviewClaim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve         bool   `json:"approve"`
		Reason          string `json:"reason"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Benefits.ReviewClaim(r.Context(), principal(r).Actor, r.PathValue("id"), body.Approve, body.Reason, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) registerResource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ResourceType string `json:"resource_type"`
		SerialNumber string `json:"serial_number"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Resources.Register(r.Context(), principal(r).Actor, body.ResourceType, body.SerialNumber)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) assignResource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ResidentID      string `json:"resident_id"`
		DueAt           string `json:"due_at"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	dueAt, err := parseTime(body.DueAt, "due_at")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.services.Resources.Assign(r.Context(), principal(r).Actor, r.PathValue("id"), body.ResidentID, dueAt, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) returnResource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Quarantine      bool  `json:"quarantine"`
		ExpectedVersion int64 `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Resources.Return(r.Context(), principal(r).Actor, r.PathValue("id"), body.Quarantine, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) openEscalation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ResidentID string          `json:"resident_id"`
		VisitID    string          `json:"visit_id"`
		Severity   domain.Severity `json:"severity"`
		Summary    string          `json:"summary"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Escalations.Open(r.Context(), principal(r).Actor, body.ResidentID, body.VisitID, body.Severity, body.Summary)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) acknowledgeEscalation(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Escalations.Acknowledge(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}
func (a *API) resolveEscalation(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Escalations.Resolve(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	result, err := a.services.Audit.List(r.Context(), principal(r).Actor, r.PathValue("type"), r.PathValue("id"), limit, offset)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func validationError(field, reason string) error { return apperr.Validation(field, reason) }
