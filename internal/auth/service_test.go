package auth

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/apperr"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
)

type authRepo struct {
	mu        sync.Mutex
	users     map[string]domain.User
	sessions  map[string]domain.Session
	tokenToID map[string]string
	touches   int
}

func newAuthRepo(users ...domain.User) *authRepo {
	repo := &authRepo{users: map[string]domain.User{}, sessions: map[string]domain.Session{}, tokenToID: map[string]string{}}
	for _, user := range users {
		repo.users[user.ID] = user
	}
	return repo
}

func (r *authRepo) UserByEmail(_ context.Context, districtID, email string) (domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.DistrictID == districtID && u.Email == email {
			return u, nil
		}
	}
	return domain.User{}, sql.ErrNoRows
}
func (r *authRepo) UserByID(_ context.Context, id string) (domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return domain.User{}, sql.ErrNoRows
	}
	return u, nil
}
func (r *authRepo) CreateSession(_ context.Context, s domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
	r.tokenToID[s.TokenHash] = s.ID
	return nil
}
func (r *authRepo) SessionAndUserByTokenHash(_ context.Context, hash string) (domain.Session, domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.tokenToID[hash]
	if !ok {
		return domain.Session{}, domain.User{}, sql.ErrNoRows
	}
	s := r.sessions[id]
	u, ok := r.users[s.UserID]
	if !ok {
		return domain.Session{}, domain.User{}, sql.ErrNoRows
	}
	return s, u, nil
}
func (r *authRepo) TouchSession(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok || s.RevokedAt != nil || !at.Before(s.ExpiresAt) {
		return sql.ErrNoRows
	}
	s.LastSeenAt = at
	r.sessions[id] = s
	r.touches++
	return nil
}
func (r *authRepo) RevokeSession(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok || s.RevokedAt != nil {
		return sql.ErrNoRows
	}
	s.RevokedAt = &at
	r.sessions[id] = s
	return nil
}
func (r *authRepo) RevokeAllUserSessions(_ context.Context, userID string, at time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for id, s := range r.sessions {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt = &at
			r.sessions[id] = s
			count++
		}
	}
	return count, nil
}

func authUser(t *testing.T, now time.Time, role domain.Role) domain.User {
	t.Helper()
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	return domain.User{ID: "user_1", DistrictID: "district_1", Email: "user@example.test", PasswordHash: hash, DisplayName: "测试用户", Role: role, Active: true, CreatedAt: now, UpdatedAt: now}
}

func TestLoginAuthenticateLogoutLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	fixed := clock.NewFixed(now)
	repo := newAuthRepo(authUser(t, now, domain.RoleCoordinator))
	service := NewService(repo, &idgen.Sequence{}, fixed, time.Hour)
	login, err := service.Login(context.Background(), LoginRequest{DistrictID: "district_1", Email: "USER@EXAMPLE.TEST", Password: "correct horse battery"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.Token == "" || !login.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("login=%+v", login)
	}
	principal, err := service.Authenticate(context.Background(), login.Token, "request_1")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if principal.Actor.UserID != "user_1" || principal.Actor.Role != domain.RoleCoordinator || principal.Actor.RequestID != "request_1" {
		t.Fatalf("principal=%+v", principal)
	}
	if err := service.Logout(context.Background(), principal); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), login.Token, "request_2"); !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("post-logout err=%v", err)
	}
}

func TestLoginRejectsInvalidCredentialsWithoutLeakingAccount(t *testing.T) {
	now := time.Now().UTC()
	repo := newAuthRepo(authUser(t, now, domain.RoleProvider))
	service := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), time.Hour)
	tests := []LoginRequest{{DistrictID: "district_1", Email: "missing@example.test", Password: "whatever password"}, {DistrictID: "district_1", Email: "user@example.test", Password: "wrong password"}, {DistrictID: "district_other", Email: "user@example.test", Password: "correct horse battery"}, {DistrictID: "district_1", Email: "not an email", Password: "correct horse battery"}}
	for i, request := range tests {
		_, err := service.Login(context.Background(), request)
		if !apperr.IsCode(err, apperr.CodeUnauthorized) {
			t.Fatalf("case %d err=%v", i, err)
		}
		app, _ := apperr.As(err)
		if app.Message != "email or password is incorrect" {
			t.Fatalf("case %d message=%q", i, app.Message)
		}
	}
}

func TestAuthenticationExpiresAndTouchesLongLivedSession(t *testing.T) {
	now := time.Now().UTC()
	fixed := clock.NewFixed(now)
	repo := newAuthRepo(authUser(t, now, domain.RoleCoordinator))
	service := NewService(repo, &idgen.Sequence{}, fixed, 2*time.Hour)
	login, err := service.Login(context.Background(), LoginRequest{DistrictID: "district_1", Email: "user@example.test", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	fixed.Advance(2 * time.Minute)
	if _, err := service.Authenticate(context.Background(), login.Token, "request_1"); err != nil {
		t.Fatal(err)
	}
	if repo.touches != 1 {
		t.Fatalf("touches=%d", repo.touches)
	}
	fixed.Advance(2 * time.Hour)
	if _, err := service.Authenticate(context.Background(), login.Token, "request_2"); !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("expired err=%v", err)
	}
}

func TestInactiveUserCannotUseExistingSession(t *testing.T) {
	now := time.Now().UTC()
	fixed := clock.NewFixed(now)
	user := authUser(t, now, domain.RoleProvider)
	repo := newAuthRepo(user)
	service := NewService(repo, &idgen.Sequence{}, fixed, time.Hour)
	login, err := service.Login(context.Background(), LoginRequest{DistrictID: "district_1", Email: "user@example.test", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	user.Active = false
	repo.users[user.ID] = user
	repo.mu.Unlock()
	if _, err := service.Authenticate(context.Background(), login.Token, "request_1"); !apperr.IsCode(err, apperr.CodeUnauthorized) {
		t.Fatalf("inactive err=%v", err)
	}
}

func TestRevokeUserRequiresRoleAndDistrict(t *testing.T) {
	now := time.Now().UTC()
	fixed := clock.NewFixed(now)
	target := authUser(t, now, domain.RoleProvider)
	repo := newAuthRepo(target)
	service := NewService(repo, &idgen.Sequence{}, fixed, time.Hour)
	login, err := service.Login(context.Background(), LoginRequest{DistrictID: "district_1", Email: "user@example.test", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	_ = login
	providerActor := domain.Actor{UserID: "provider_admin", DistrictID: "district_1", Role: domain.RoleProvider, RequestID: "r"}
	if _, err := service.RevokeUser(context.Background(), providerActor, target.ID); !apperr.IsCode(err, apperr.CodeForbidden) {
		t.Fatalf("provider revoke err=%v", err)
	}
	otherDistrict := domain.Actor{UserID: "auditor", DistrictID: "district_other", Role: domain.RoleAuditor, RequestID: "r"}
	if _, err := service.RevokeUser(context.Background(), otherDistrict, target.ID); !apperr.IsCode(err, apperr.CodeNotFound) {
		t.Fatalf("cross-district revoke err=%v", err)
	}
	coordinator := domain.Actor{UserID: "coordinator", DistrictID: "district_1", Role: domain.RoleCoordinator, RequestID: "r"}
	count, err := service.RevokeUser(context.Background(), coordinator, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count=%d", count)
	}
}

func TestHashPasswordValidationAndComparison(t *testing.T) {
	for _, password := range []string{"", "short", "123456789"} {
		if _, err := HashPassword(password); err == nil {
			t.Fatalf("password %q accepted", password)
		}
	}
	tooLong := make([]byte, 129)
	for i := range tooLong {
		tooLong[i] = 'a'
	}
	if _, err := HashPassword(string(tooLong)); err == nil {
		t.Fatal("long password accepted")
	}
	hash, err := HashPassword("valid password 123")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "valid password 123" {
		t.Fatal("password stored in clear text")
	}
}

func TestContextCancellationStopsLogin(t *testing.T) {
	now := time.Now().UTC()
	repo := newAuthRepo(authUser(t, now, domain.RoleCoordinator))
	service := NewService(repo, &idgen.Sequence{}, clock.NewFixed(now), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Login(ctx, LoginRequest{DistrictID: "district_1", Email: "user@example.test", Password: "correct horse battery"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
