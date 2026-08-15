package tlsfingerprint

import "testing"

func TestBuiltInDefaultProfile_ReturnsCompleteIndependentCopies(t *testing.T) {
	first := BuiltInDefaultProfile()
	second := BuiltInDefaultProfile()

	if first.Name != BuiltInDefaultProfileName {
		t.Fatalf("profile name = %q, want %q", first.Name, BuiltInDefaultProfileName)
	}
	if len(first.CipherSuites) != 17 || len(first.Curves) != 3 || len(first.Extensions) != 14 {
		t.Fatalf("incomplete profile: ciphers=%d curves=%d extensions=%d", len(first.CipherSuites), len(first.Curves), len(first.Extensions))
	}
	if len(first.ALPNProtocols) != 0 {
		t.Fatalf("ALPN must be resolved by the selected transport, got %v", first.ALPNProtocols)
	}

	first.CipherSuites[0] = 0
	first.Curves[0] = 0
	first.Extensions[0] = 999
	if second.CipherSuites[0] == 0 || second.Curves[0] == 0 || second.Extensions[0] == 999 {
		t.Fatal("BuiltInDefaultProfile returned shared mutable slices")
	}
}
