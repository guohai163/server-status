package store

import "testing"

func TestNonNilStringsConvertsMissingJSONArraysForPostgres(t *testing.T) {
	values := nonNilStrings(nil)
	if values == nil || len(values) != 0 {
		t.Fatalf("expected a non-nil empty slice, got %#v", values)
	}

	existing := []string{"rw"}
	values = nonNilStrings(existing)
	if len(values) != 1 || values[0] != "rw" {
		t.Fatalf("expected existing values to be preserved, got %#v", values)
	}
}
