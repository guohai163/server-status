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

func TestPostgresMACAddressAcceptsOnlySixByteAddresses(t *testing.T) {
	if got := postgresMACAddress("00-11-22-33-44-55"); got != "00:11:22:33:44:55" {
		t.Fatalf("unexpected normalized MAC address %q", got)
	}
	if got := postgresMACAddress("00:00:00:00:00:00:00:e0"); got != "" {
		t.Fatalf("expected EUI-64 address to be omitted, got %q", got)
	}
	if got := postgresMACAddress("not-a-mac"); got != "" {
		t.Fatalf("expected invalid address to be omitted, got %q", got)
	}
}
