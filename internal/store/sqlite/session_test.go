package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

func TestSessionCreateTouchRevokeAndDelete(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	user := seedUser(t, store, "user_1", domain.RoleCoordinator, now)
	session := domain.Session{ID: "session_1", UserID: user.ID, TokenHash: "hash_1", ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now}
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	loaded, loadedUser, err := store.SessionAndUserByTokenHash(context.Background(), "hash_1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != session.ID || loadedUser.ID != user.ID {
		t.Fatalf("session=%+v user=%+v", loaded, loadedUser)
	}
	if err := store.TouchSession(context.Background(), session.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeSession(context.Background(), session.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	loaded, _, _ = store.SessionAndUserByTokenHash(context.Background(), "hash_1")
	if loaded.RevokedAt == nil {
		t.Fatal("revocation not persisted")
	}
	if err := store.TouchSession(context.Background(), session.ID, now.Add(3*time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("touch revoked err=%v", err)
	}
	if count, err := store.DeleteExpiredSessions(context.Background(), now.Add(25*time.Hour)); err != nil || count != 1 {
		t.Fatalf("delete count=%d err=%v", count, err)
	}
}

func TestRevokeAllUserSessions(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	seedDistrict(t, store, now)
	user := seedUser(t, store, "user_1", domain.RoleProvider, now)
	for i, hash := range []string{"hash_a", "hash_b"} {
		session := domain.Session{ID: "session_" + string(rune('a'+i)), UserID: user.ID, TokenHash: hash, ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now}
		if err := store.CreateSession(context.Background(), session); err != nil {
			t.Fatal(err)
		}
	}
	count, err := store.RevokeAllUserSessions(context.Background(), user.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("revoked=%d", count)
	}
	count, err = store.RevokeAllUserSessions(context.Background(), user.ID, now.Add(2*time.Minute))
	if err != nil || count != 0 {
		t.Fatalf("second revoked=%d err=%v", count, err)
	}
}
