package domain

import "testing"

func TestVersionStatusValid(t *testing.T) {
	for _, s := range []VersionStatus{VersionUploaded, VersionProcessing, VersionReady, VersionFailed} {
		if !s.Valid() {
			t.Fatalf("valid status rejected: %s", s)
		}
	}
	for _, s := range []VersionStatus{"", "indexed", "UPLOADED"} {
		if s.Valid() {
			t.Fatalf("invalid status accepted: %s", s)
		}
	}
}
