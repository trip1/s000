package auth

import (
	"testing"
	"time"
)

func TestPersonalAccessTokenManagerIssueListVerifyAndRevoke(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 19, 13, 0, 0, 0, time.UTC)
	m := NewPersonalAccessTokenManager([]byte("signing-key"), func() time.Time { return now })

	token, issued, err := m.Issue("cli-user", time.Hour, "local shell")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if issued.Subject != "cli-user" || issued.ID == "" {
		t.Fatalf("unexpected issued token metadata: %#v", issued)
	}

	list := m.List()
	if len(list) != 1 || list[0].ID != issued.ID {
		t.Fatalf("expected one listed token, got %#v", list)
	}

	subject, err := m.Verify(token)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if subject != "cli-user" {
		t.Fatalf("expected subject cli-user, got %q", subject)
	}

	if err := m.Revoke(issued.ID); err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	if _, err := m.Verify(token); err != ErrInvalidPersonalAccessToken {
		t.Fatalf("expected invalid token after revoke, got %v", err)
	}
}
