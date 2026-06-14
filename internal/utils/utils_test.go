package utils

import "testing"

func TestIsMainBranch(t *testing.T) {
	tests := []struct {
		branch string
		want   bool
	}{
		{"main", true},
		{"master", true},
		{"develop", false},
		{"feature/foo", false},
		{"", false},
		{"Main", false},
	}
	for _, tt := range tests {
		if got := IsMainBranch(tt.branch); got != tt.want {
			t.Errorf("IsMainBranch(%q) = %v, want %v", tt.branch, got, tt.want)
		}
	}
}

func TestGetSanitizedNamespace(t *testing.T) {
	tests := []struct {
		namespace string
		branch    string
		want      string
	}{
		{"myapp", "main", "myapp"},
		{"myapp", "master", "myapp"},
		{"MyApp", "main", "myapp"},
		{"myapp", "feature/login", "myapp-feature-login"},
		{"myapp", "fix_bug", "myapp-fix-bug"},
		{"myapp", "dev branch", "myapp-dev-branch"},
		{"myapp", "test#1!", "myapp-test1"},
		{"myapp", "v1.0.0", "myapp-v100"},
	}
	for _, tt := range tests {
		got := GetSanitizedNamespace(tt.namespace, tt.branch)
		if got != tt.want {
			t.Errorf("GetSanitizedNamespace(%q, %q) = %q, want %q",
				tt.namespace, tt.branch, got, tt.want)
		}
	}
}
