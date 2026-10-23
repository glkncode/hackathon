package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoleValid(t *testing.T) {
	for _, tc := range []struct {
		role Role
		want bool
	}{{RoleAdmin, true}, {RoleMember, true}, {"", false}, {"owner", false}, {"ADMIN", false}} {
		if got := tc.role.Valid(); got != tc.want {
			t.Errorf("Role(%q).Valid() = %v", tc.role, got)
		}
	}
}

func TestSensitiveFieldsExcludedFromJSON(t *testing.T) {
	for _, model := range []any{
		User{Username: "member", PasswordHash: "private-hash"},
		DocumentVersion{StorageKey: "private-storage-key"},
		Session{TokenHash: []byte("token-hash"), CSRFTokenHash: []byte("csrf-hash")},
	} {
		data, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"private-hash", "private-storage-key", "StorageKey", "storage_key", "password", "token", "Hash"} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("JSON contains sensitive field %q: %s", forbidden, data)
			}
		}
	}
}
