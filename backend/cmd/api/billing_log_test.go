package main

import "testing"

func TestBillingEnvLogAttrsOmittedWhenSelfHosted(t *testing.T) {
	if got := billingEnvLogAttrs(true, "sandbox"); len(got) != 0 {
		t.Fatalf("self-host must not log billing_env, got %v", got)
	}
	got := billingEnvLogAttrs(false, "live")
	if len(got) != 2 || got[0] != "billing_env" || got[1] != "live" {
		t.Fatalf("cloud attrs = %v, want [billing_env live]", got)
	}
}
