package app

import "testing"

func TestIsDeviceLibraryUUID(t *testing.T) {
	if !isDeviceLibraryUUID("87bb635d0b2f489a9f60e7cd225beb3c") {
		t.Fatal("32-character hexadecimal device uuid should be accepted")
	}
	for _, got := range []string{
		"2116294927a134e2",                 // placed-instance uuid from sch list
		"87bb635d0b2f489a9f60e7cd225beb3",  // 31 chars
		"87bb635d0b2f489a9f60e7cd225beb3z", // non-hex
	} {
		if isDeviceLibraryUUID(got) {
			t.Errorf("%q should be rejected as a device-library uuid", got)
		}
	}
}
