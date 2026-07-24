package main

import "testing"

func TestWindowsProductNamePrefersRegistryValue(t *testing.T) {
	if got := windowsProductName("Windows Server 2012 Standard", 6, 2); got != "Windows Server 2012 Standard" {
		t.Fatalf("windowsProductName() = %q", got)
	}
}

func TestWindowsProductNameFallsBackToVersionMapping(t *testing.T) {
	tests := []struct {
		major, minor uint32
		want         string
	}{
		{major: 6, minor: 1, want: "Windows Server 2008 R2"},
		{major: 6, minor: 2, want: "Windows Server 2012"},
		{major: 6, minor: 3, want: "Windows Server 2012 R2"},
		{major: 10, minor: 0, want: "Windows"},
	}
	for _, test := range tests {
		if got := windowsProductName("", test.major, test.minor); got != test.want {
			t.Errorf("windowsProductName(%d, %d) = %q, want %q", test.major, test.minor, got, test.want)
		}
	}
}
