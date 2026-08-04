package release

import (
	"encoding/json"
	"testing"
)

func TestTrustedVerificationKeyset(t *testing.T) {
	keyset, err := TrustedVerificationKeyset()
	if err != nil {
		t.Fatal(err)
	}
	if len(keyset.Keys) != 1 || keyset.Keys[0].KeyID != "helper-alpha-2026-08" || keyset.Keys[0].Status != "active" {
		t.Fatal("embedded Helper release verification keyset differs from the Paid Alpha contract")
	}
}

func TestVerificationKeysetRejectsInvalidState(t *testing.T) {
	trusted, err := TrustedVerificationKeyset()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*VerificationKeyset)
	}{
		{name: "duplicate", mutate: func(value *VerificationKeyset) { value.Keys = append(value.Keys, value.Keys[0]) }},
		{name: "revoked only", mutate: func(value *VerificationKeyset) { value.Keys[0].Status = "revoked" }},
		{name: "wrong usage", mutate: func(value *VerificationKeyset) { value.Keys[0].Usage = "theme-release" }},
		{name: "bad public key", mutate: func(value *VerificationKeyset) { value.Keys[0].PublicKeyBase64 = "AAAA" }},
		{name: "backwards validity", mutate: func(value *VerificationKeyset) { value.Keys[0].NotAfter = value.Keys[0].NotBefore }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := trusted
			candidate.Keys = append([]VerificationKey(nil), trusted.Keys...)
			test.mutate(&candidate)
			raw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseVerificationKeyset(append(raw, '\n')); err == nil {
				t.Fatal("invalid Helper release verification keyset was accepted")
			}
		})
	}
}
