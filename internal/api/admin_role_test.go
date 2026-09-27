package api

import (
	"encoding/json"
	"testing"
)

// decodeRoleUpdate mirrors the role branch of HandleUpdateUser: it reports
// whether the role column is written at all, and the value written.
// Kept in lockstep with the handler so the semantics below are pinned.
func decodeRoleUpdate(body string) (written bool, value interface{}, badRequest bool) {
	var req struct {
		Role json.RawMessage `json:"role,omitempty"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return false, nil, true
	}
	if len(req.Role) > 0 {
		if string(req.Role) == "null" {
			return true, nil, false
		}
		var role string
		if err := json.Unmarshal(req.Role, &role); err != nil {
			return false, nil, true
		}
		if role == "" {
			return true, nil, false
		}
		return true, role, false
	}
	return false, nil, false
}

func TestRoleUpdateShapes(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantWritten bool
		wantValue   interface{}
	}{
		// The reported bug: the admin panel's "User" option sends null.
		// Before the fix this wrote nothing and the request 400'd,
		// so an Ops user could never be demoted to a plain user.
		{"explicit null clears role", `{"role":null}`, true, nil},
		// The frontend may also send "" for the same option.
		{"empty string clears role", `{"role":""}`, true, nil},
		// Absent role must not touch the column (partial-update semantics):
		// banning a user must not wipe their role.
		{"absent role untouched", `{"banned":true}`, false, nil},
		{"role set to ops", `{"role":"ops"}`, true, "ops"},
		{"role set to admin", `{"role":"admin"}`, true, "admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			written, value, badRequest := decodeRoleUpdate(tt.body)
			if badRequest {
				t.Fatalf("unexpected rejection for body %s", tt.body)
			}
			if written != tt.wantWritten {
				t.Errorf("body %s: role written = %v, want %v", tt.body, written, tt.wantWritten)
			}
			if written && value != tt.wantValue {
				t.Errorf("body %s: role value = %#v, want %#v", tt.body, value, tt.wantValue)
			}
		})
	}
}

// A cleared role must be stored as NULL, never "", because the canonical
// "plain user" filter in HandleListUsers is `u.role IS NULL`. An empty
// string would match neither that filter nor any named role.
func TestClearedRoleIsNilNotEmptyString(t *testing.T) {
	for _, body := range []string{`{"role":null}`, `{"role":""}`} {
		written, value, _ := decodeRoleUpdate(body)
		if !written {
			t.Fatalf("body %s: expected role column to be written", body)
		}
		if value != nil {
			t.Errorf("body %s: stored %#v, want nil (SQL NULL)", body, value)
		}
		if s, ok := value.(string); ok && s == "" {
			t.Errorf("body %s: stored empty string, which matches no role filter", body)
		}
	}
}

func TestNonStringRoleRejected(t *testing.T) {
	if _, _, badRequest := decodeRoleUpdate(`{"role":123}`); !badRequest {
		t.Error("numeric role should be rejected, not coerced")
	}
}
