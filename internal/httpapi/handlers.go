package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/auth"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/resident"
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
)

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DistrictID string `json:"district_id"`
		Email      string `json:"email"`
		Password   string `json:"password"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Auth.Login(r.Context(), auth.LoginRequest{DistrictID: body.DistrictID, Email: body.Email, Password: body.Password})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.services.Auth.Logout(r.Context(), principal(r)); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	a.result(w, http.StatusOK, principal(r).User)
}

func (a *API) createResident(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExternalRef string `json:"external_ref"`
		FullName    string `json:"full_name"`
		HouseholdID string `json:"household_id"`
		BirthDate   string `json:"birth_date"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	birthDate, err := time.Parse("2006-01-02", body.BirthDate)
	if err != nil {
		a.writeError(w, r, validation("birth_date", "must use YYYY-MM-DD"))
		return
	}
	result, err := a.services.Residents.Create(r.Context(), principal(r).Actor, resident.CreateInput{ExternalRef: body.ExternalRef, FullName: body.FullName, HouseholdID: body.HouseholdID, BirthDate: birthDate})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) getResident(w http.ResponseWriter, r *http.Request) {
	result, err := a.services.Residents.Get(r.Context(), principal(r).Actor, r.PathValue("id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) listResidents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := a.services.Residents.List(r.Context(), principal(r).Actor, storesqlite.ResidentFilter{Household: r.URL.Query().Get("household_id"), Consent: domain.ConsentStatus(r.URL.Query().Get("consent")), Limit: limit, Offset: offset})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (a *API) grantConsent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpiresAt       string `json:"expires_at"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	if err := requireVersion(body.ExpectedVersion); err != nil {
		a.writeError(w, r, err)
		return
	}
	expiresAt, err := parseTime(body.ExpiresAt, "expires_at")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.services.Residents.GrantConsent(r.Context(), principal(r).Actor, r.PathValue("id"), expiresAt, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) withdrawConsent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Residents.WithdrawConsent(r.Context(), principal(r).Actor, r.PathValue("id"), body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) startAssessment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SupportLevel int    `json:"support_level"`
		ValidUntil   string `json:"valid_until"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	validUntil, err := parseTime(body.ValidUntil, "valid_until")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.services.Eligibility.Start(r.Context(), principal(r).Actor, r.PathValue("id"), body.SupportLevel, validUntil)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) addAssessmentEvidence(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key             string `json:"key"`
		Value           string `json:"value"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Eligibility.AddEvidence(r.Context(), principal(r).Actor, r.PathValue("id"), body.Key, body.Value, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) submitAssessment(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Eligibility.Submit(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) approveAssessment(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Eligibility.Approve(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) createPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AssessmentID string `json:"assessment_id"`
		StartsAt     string `json:"starts_at"`
		EndsAt       string `json:"ends_at"`
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
	result, err := a.services.Plans.Create(r.Context(), principal(r).Actor, r.PathValue("id"), body.AssessmentID, startsAt, endsAt)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) addPlanGoal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Goal            string `json:"goal"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Plans.AddGoal(r.Context(), principal(r).Actor, r.PathValue("id"), body.Goal, body.ExpectedVersion)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) submitPlan(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Plans.Submit(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}
func (a *API) activatePlan(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Plans.Activate(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) registerProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           string   `json:"name"`
		Capabilities   []string `json:"capabilities"`
		CapacityPerDay int      `json:"capacity_per_day"`
	}
	if !a.bind(w, r, &body) {
		return
	}
	result, err := a.services.Providers.Register(r.Context(), principal(r).Actor, body.Name, body.Capabilities, body.CapacityPerDay)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) activateProvider(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Providers.Activate(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) createAuthorization(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServiceCode    string `json:"service_code"`
		Units          int    `json:"units"`
		UnitPriceCents int64  `json:"unit_price_cents"`
		StartsAt       string `json:"starts_at"`
		EndsAt         string `json:"ends_at"`
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
	result, err := a.services.Benefits.Authorize(r.Context(), principal(r).Actor, r.PathValue("id"), body.ServiceCode, body.Units, body.UnitPriceCents, startsAt, endsAt)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusCreated, result)
}

func (a *API) activateAuthorization(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionBody(w, r)
	if !ok {
		return
	}
	result, err := a.services.Benefits.Activate(r.Context(), principal(r).Actor, r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.result(w, http.StatusOK, result)
}

func (a *API) versionBody(w http.ResponseWriter, r *http.Request) (int64, bool) {
	var body struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	if !a.bind(w, r, &body) {
		return 0, false
	}
	if err := requireVersion(body.ExpectedVersion); err != nil {
		a.writeError(w, r, err)
		return 0, false
	}
	return body.ExpectedVersion, true
}

func validation(field, reason string) error { return validationError(field, reason) }
