package main

import (
	"os"
	"testing"
)

func TestRegistrationOption(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		args      []string
		want      bool
		wantError bool
	}{
		{name: "default disabled"},
		{name: "environment enabled", env: "true", want: true},
		{name: "environment disabled", env: "false"},
		{name: "flag enabled", args: []string{"--allow-registration"}, want: true},
		{name: "flag overrides false", env: "false", args: []string{"--allow-registration"}, want: true},
		{name: "flag overrides true", env: "true", args: []string{"--allow-registration=false"}},
		{name: "invalid environment", env: "typo", wantError: true},
		{name: "invalid flag", args: []string{"--allow-registration=typo"}, wantError: true},
		{name: "unexpected argument", args: []string{"unexpected"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ALLOW_REGISTRATION", tc.env)
			if tc.env == "" {
				if err := os.Unsetenv("ALLOW_REGISTRATION"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := registrationOption(tc.args)
			if (err != nil) != tc.wantError || got != tc.want {
				t.Fatalf("registrationOption = %v, %v; want %v, error=%v", got, err, tc.want, tc.wantError)
			}
		})
	}
}
