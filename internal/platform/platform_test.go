package platform

import "testing"

func TestIsXKS(t *testing.T) {
	t.Setenv(PlatformTypeEnv, "")
	if IsXKS() {
		t.Fatal("expected false when ODH_PLATFORM_TYPE is unset")
	}

	t.Setenv(PlatformTypeEnv, "OpenDataHub")
	if IsXKS() {
		t.Fatal("expected false for OpenDataHub platform")
	}

	t.Setenv(PlatformTypeEnv, XKS)
	if !IsXKS() {
		t.Fatal("expected true for xKS platform")
	}
}
