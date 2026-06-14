package kubernetes

import "testing"

func TestGenerateRandomChars(t *testing.T) {
	s, err := GenerateRandomChars()
	if err != nil {
		t.Fatalf("GenerateRandomChars() error = %v", err)
	}
	// 8 random bytes -> 16 hex characters
	const expectedLen = 16
	if len(s) != expectedLen {
		t.Errorf("GenerateRandomChars() len = %d, want %d", len(s), expectedLen)
	}

	// Ensure two calls produce different values
	s2, err := GenerateRandomChars()
	if err != nil {
		t.Fatalf("GenerateRandomChars() error = %v", err)
	}
	if s == s2 {
		t.Errorf("GenerateRandomChars() produced duplicate values: %q", s)
	}
}
