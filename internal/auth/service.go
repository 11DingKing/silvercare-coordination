package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/apperr"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"golang.org/x/crypto/bcrypt"
)

type Repository interface {
	UserByEmail(context.Context, string, string) (domain.User, error)
	UserByID(context.Context, string) (domain.User, error)
	CreateSession(context.Context, domain.Session) error
	SessionAndUserByTokenHash(context.Context, string) (domain.Session, domain.User, error)
	TouchSession(context.Context, string, time.Time) error
	RevokeSession(context.Context, string, time.Time) error
	RevokeAllUserSessions(context.Context, string, time.Time) (int64, error)
}

type Service struct {
	repo Repository
	ids  idgen.Generator
	now  clock.Clock
	ttl  time.Duration
}

type LoginRequest struct {
	DistrictID string
	Email      string
	Password   string
}

type LoginResult struct {
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expires_at"`
	User      PublicUser `json:"user"`
}

type PublicUser struct {
	ID          string      `json:"id"`
	DistrictID  string      `json:"district_id"`
	Email       string      `json:"email"`
	DisplayName string      `json:"display_name"`
	Role        domain.Role `json:"role"`
}

type Principal struct {
	SessionID string
	Actor     domain.Actor
	User      PublicUser
}

func NewService(repo Repository, ids idgen.Generator, now clock.Clock, ttl time.Duration) *Service {
	return &Service{repo: repo, ids: ids, now: now, ttl: ttl}
}

func (s *Service) Login(ctx context.Context, request LoginRequest) (LoginResult, error) {
	if err := ctx.Err(); err != nil {
		return LoginResult{}, err
	}
	request.DistrictID = strings.TrimSpace(request.DistrictID)
	email, err := domain.NormalizeEmail(request.Email)
	if err != nil || request.DistrictID == "" || request.Password == "" {
		return LoginResult{}, apperr.New(apperr.CodeUnauthorized, "email or password is incorrect")
	}
	user, err := s.repo.UserByEmail(ctx, request.DistrictID, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LoginResult{}, apperr.New(apperr.CodeUnauthorized, "email or password is incorrect")
		}
		return LoginResult{}, apperr.Internal("load login user", err)
	}
	if !user.Active || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.Password)) != nil {
		return LoginResult{}, apperr.New(apperr.CodeUnauthorized, "email or password is incorrect")
	}
	token, tokenHash, err := generateToken()
	if err != nil {
		return LoginResult{}, apperr.Internal("generate session token", err)
	}
	sessionID, err := s.ids.New("session")
	if err != nil {
		return LoginResult{}, apperr.Internal("generate session id", err)
	}
	now := s.now.Now()
	session := domain.Session{
		ID: sessionID, UserID: user.ID, TokenHash: tokenHash,
		ExpiresAt: now.Add(s.ttl), CreatedAt: now, LastSeenAt: now,
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return LoginResult{}, apperr.Internal("create session", err)
	}
	return LoginResult{Token: token, ExpiresAt: session.ExpiresAt, User: publicUser(user)}, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken, requestID string) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	if rawToken == "" || requestID == "" {
		return Principal{}, apperr.New(apperr.CodeUnauthorized, "a valid session is required")
	}
	hash := hashToken(rawToken)
	session, user, err := s.repo.SessionAndUserByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Principal{}, apperr.New(apperr.CodeUnauthorized, "session is not recognized")
		}
		return Principal{}, apperr.Internal("load session", err)
	}
	now := s.now.Now()
	if !user.Active || !session.ActiveAt(now) {
		return Principal{}, apperr.New(apperr.CodeUnauthorized, "session has expired or was revoked")
	}
	if now.Sub(session.LastSeenAt) >= time.Minute {
		if err := s.repo.TouchSession(ctx, session.ID, now); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Principal{}, apperr.Internal("touch session", err)
		}
	}
	return Principal{
		SessionID: session.ID,
		Actor:     domain.Actor{UserID: user.ID, DistrictID: user.DistrictID, Role: user.Role, RequestID: requestID},
		User:      publicUser(user),
	}, nil
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	if err := s.repo.RevokeSession(ctx, principal.SessionID, s.now.Now()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return apperr.Internal("revoke session", err)
	}
	return nil
}

func (s *Service) RevokeUser(ctx context.Context, actor domain.Actor, targetUserID string) (int64, error) {
	if actor.Role != domain.RoleCoordinator && actor.Role != domain.RoleAuditor {
		return 0, apperr.New(apperr.CodeForbidden, "this role cannot revoke user sessions")
	}
	user, err := s.repo.UserByID(ctx, targetUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, apperr.NotFound("user", targetUserID)
		}
		return 0, apperr.Internal("load session target", err)
	}
	if user.DistrictID != actor.DistrictID {
		return 0, apperr.NotFound("user", targetUserID)
	}
	count, err := s.repo.RevokeAllUserSessions(ctx, targetUserID, s.now.Now())
	if err != nil {
		return 0, apperr.Internal("revoke user sessions", err)
	}
	return count, nil
}

func HashPassword(password string) (string, error) {
	if len(password) < 10 || len(password) > 128 {
		return "", fmt.Errorf("password length must be between 10 and 128 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func generateToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func publicUser(user domain.User) PublicUser {
	return PublicUser{ID: user.ID, DistrictID: user.DistrictID, Email: user.Email, DisplayName: user.DisplayName, Role: user.Role}
}
