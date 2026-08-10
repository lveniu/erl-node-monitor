package sshprobe

import (
	"strings"
	"testing"
)

func TestDiagnosticTermsRedactRoleIdentifiers(t *testing.T) {
	input := `[{<1.2.3>,123,[role_20182701346400,map_0_190013_20182701324300]}]`
	result := redactDiagnosticTerm(input)
	if strings.Contains(result, "20182701346400") || strings.Contains(result, "20182701324300") || !strings.Contains(result, "role_[redacted]") {
		t.Fatalf("role identifier was not redacted: %s", result)
	}
}
