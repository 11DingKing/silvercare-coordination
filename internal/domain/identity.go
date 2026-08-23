package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Role string

const (
	RoleCoordinator Role = "coordinator"
	RoleProvider    Role = "provider"
	RoleAuditor     Role = "auditor"
)

func ParseRole(value string) (Role, error) {
	role := Role(strings.ToLower(strings.TrimSpace(value)))
	switch role {
	case RoleCoordinator, RoleProvider, RoleAuditor:
		return role, nil
	default:
		return "", fmt.Errorf("unknown role %q", value)
	}
}

func (r Role) CanManageResidents() bool { return r == RoleCoordinator }
func (r Role) CanDeliverVisits() bool   { return r == RoleProvider }
func (r Role) CanReviewClaims() bool    { return r == RoleAuditor || r == RoleCoordinator }
func (r Role) CanReadAudit() bool       { return r == RoleAuditor }

type Actor struct {
	UserID     string
	DistrictID string
	Role       Role
	RequestID  string
}

func (a Actor) Validate() error {
	if a.UserID == "" || a.DistrictID == "" || a.RequestID == "" {
		return fmt.Errorf("actor identity is incomplete")
	}
	_, err := ParseRole(string(a.Role))
	return err
}

type User struct {
	ID           string
	DistrictID   string
	Email        string
	PasswordHash string
	DisplayName  string
	Role         Role
	Active       bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func NormalizeEmail(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if len(normalized) > 254 || !emailPattern.MatchString(normalized) {
		return "", fmt.Errorf("email is invalid")
	}
	return normalized, nil
}

type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}

func (s Session) ActiveAt(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}
