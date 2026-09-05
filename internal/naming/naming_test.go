package naming

import (
	"strings"
	"testing"
)

func TestExported(t *testing.T) {
	tests := map[string]string{
		"user_id":           "UserID",
		"userId":            "UserID",
		"UserID":            "UserID",
		"api-key":           "APIKey",
		"api_keys":          "APIKeys",
		"url":               "URL",
		"http_status":       "HTTPStatus",
		"parseHTTPResponse": "ParseHTTPResponse",
		"created_at":        "CreatedAt",
		"2fa_enabled":       "N2faEnabled",
		"a.b.c":             "ABC",
		"in progress":       "InProgress",
		"":                  "Unnamed",
		"---":               "Unnamed",
		"uuid":              "UUID",
		"oauth2_token":      "Oauth2Token",
		"IPAddress":         "IPAddress",
	}
	for in, want := range tests {
		if got := Exported(in); got != want {
			t.Errorf("Exported(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnexported(t *testing.T) {
	tests := map[string]string{
		"user_id":  "userID",
		"ID":       "id",
		"type":     "type_",
		"range":    "range_",
		"string":   "string_",
		"len":      "len_",
		"api_key":  "apiKey",
		"HTTPPort": "httpPort",
	}
	for in, want := range tests {
		if got := Unexported(in); got != want {
			t.Errorf("Unexported(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWords(t *testing.T) {
	tests := map[string]string{
		"parseHTTPResponse": "parse,HTTP,Response",
		"user_id":           "user,id",
		"HTTPServer":        "HTTP,Server",
		"a1b2":              "a1b2",
		"snake_and-kebab":   "snake,and,kebab",
	}
	for in, want := range tests {
		if got := strings.Join(Words(in), ","); got != want {
			t.Errorf("Words(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnumConst(t *testing.T) {
	tests := []struct{ typ, val, want string }{
		{"Status", "in_progress", "StatusInProgress"},
		{"Status", "OK", "StatusOK"},
		{"Status", "", "StatusEmpty"},
		{"Color", "red-ish", "ColorRedIsh"},
	}
	for _, tt := range tests {
		if got := EnumConst(tt.typ, tt.val); got != tt.want {
			t.Errorf(
				"EnumConst(%q, %q) = %q, want %q",
				tt.typ,
				tt.val,
				got,
				tt.want,
			)
		}
	}
}

func TestDeduper(t *testing.T) {
	d := NewDeduper("Type")
	got := []string{
		d.Take("Name"), d.Take("Name"), d.Take("Name"), d.Take("Type"),
	}
	want := []string{"Name", "Name2", "Name3", "Type2"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Take #%d = %q, want %q", i, got[i], want[i])
		}
	}
	// The suffix must keep counting correctly past a single digit.
	d2 := NewDeduper()
	for i := 0; i < 11; i++ {
		d2.Take("X")
	}
	if got := d2.Take("X"); got != "X12" {
		t.Errorf("12th duplicate = %q, want X12", got)
	}
}
