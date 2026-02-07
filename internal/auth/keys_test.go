package auth

import "testing"

func TestValidateUsername(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{in: "", wantErr: true},
		{in: "ab", wantErr: true},        // too short
		{in: "a_b", wantErr: false},      // ok
		{in: "a-b", wantErr: false},      // ok
		{in: "a0_", wantErr: false},      // ok
		{in: "0abc", wantErr: true},      // must start with letter
		{in: "Abc", wantErr: true},       // lowercase only
		{in: "abc!", wantErr: true},      // invalid char
		{in: "admin", wantErr: true},     // reserved
		{in: "moderator", wantErr: true}, // reserved
		{in: "animasola", wantErr: true}, // reserved
		{in: "register", wantErr: true},  // reserved
		{in: "this_is_way_too_long_for_v1", wantErr: true},
	}

	for _, tc := range cases {
		err := ValidateUsername(tc.in)
		gotErr := err != nil
		if gotErr != tc.wantErr {
			t.Fatalf("ValidateUsername(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
		}
	}
}
