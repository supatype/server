package sesclient

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
)

// A hosted project has no ambient AWS credentials, so SES on Supatype Cloud could only ever send
// with the platform's own, or not at all. A key and region set in the server's configuration are
// what the client signs with; with none set, the ambient provider chain still applies.
func TestCredentialsAreTheProjectsOwnWhenSet(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AMBIENT")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret")
	t.Setenv("AWS_REGION", "us-east-1")

	cfg, err := config.LoadDefaultConfig(context.Background(), Credentials{AccessKeyID: "AKIAPROJECT", SecretAccessKey: "project-secret", Region: "eu-west-2"}.options()...)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "AKIAPROJECT" || got.SecretAccessKey != "project-secret" {
		t.Errorf("signed with %q, want the project's own key", got.AccessKeyID)
	}
	if cfg.Region != "eu-west-2" {
		t.Errorf("region = %q, want eu-west-2", cfg.Region)
	}
}

func TestHalfAKeyLeavesTheAmbientChain(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AMBIENT")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret")
	t.Setenv("AWS_REGION", "us-east-1")

	cfg, err := config.LoadDefaultConfig(context.Background(), Credentials{AccessKeyID: "AKIAPROJECT"}.options()...)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "AMBIENT" || cfg.Region != "us-east-1" {
		t.Errorf("got key %q region %q, want the ambient ones", got.AccessKeyID, cfg.Region)
	}
}

func TestNewBuildsAClient(t *testing.T) {
	c, err := New(context.Background(), "mail@example.com", Credentials{AccessKeyID: "A", SecretAccessKey: "S", Region: "eu-west-2"})
	if err != nil || c == nil || c.from != "mail@example.com" {
		t.Fatalf("New = %v, %v", c, err)
	}
}
