package main

import "testing"

func TestConfigValidationAndDefaults(t *testing.T) {
	config := Config{
		ServerURL: "http://central.example:8080/",
		AgentID:   "20000000-0000-4000-8000-000000000001",
		Token:     "node-token",
	}
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	if config.ServerURL != "http://central.example:8080" || config.IntervalSeconds != 60 {
		t.Fatalf("defaults were not applied: %#v", config)
	}
}

func TestConfigRejectsInvalidIdentity(t *testing.T) {
	config := Config{ServerURL: "http://central.example", AgentID: "not-a-uuid", Token: "token"}
	if err := config.validate(); err == nil {
		t.Fatal("expected invalid Agent ID to be rejected")
	}
}
