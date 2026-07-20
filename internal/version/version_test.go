package version

import "testing"

func TestCompare(t *testing.T) {
	tests := []struct {
		left, right string
		expected    int
	}{
		{"0.4.4", "0.4.5", -1},
		{"v1.2.3", "1.2.3", 0},
		{"1.10.0", "1.9.9", 1},
		{"1.2.3-rc.1", "1.2.3", -1},
		{"1.2.3-rc.2", "1.2.3-rc.10", -1},
	}
	for _, test := range tests {
		actual, ok := Compare(test.left, test.right)
		if !ok || actual != test.expected {
			t.Errorf("Compare(%q, %q) = %d, %v; expected %d, true", test.left, test.right, actual, ok, test.expected)
		}
	}
}

func TestCompareRejectsNonReleaseVersions(t *testing.T) {
	for _, value := range []string{"dev", "main", "1.2"} {
		if _, ok := Compare(value, "1.2.3"); ok {
			t.Errorf("Compare accepted %q", value)
		}
	}
}

func TestNormalizePreservesReleaseTagPath(t *testing.T) {
	for input, expected := range map[string]string{
		"v1.2.3":     "1.2.3",
		"1.2.3-rc.1": "1.2.3-rc.1",
		"1.2.3.1":    "1.2.3.1",
	} {
		actual, ok := Normalize(input)
		if !ok || actual != expected {
			t.Errorf("Normalize(%q) = %q, %v; expected %q, true", input, actual, ok, expected)
		}
	}
}
