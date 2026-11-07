package main

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestAuthenticated_exactToken_accepted(t *testing.T) {
	ctx := metadata.NewIncomingContext(
		context.Background(),
		metadata.Pairs(authHeader, "expected-token"),
	)
	if !authenticated(ctx, "expected-token") {
		t.Fatal("exact authentication token was rejected")
	}
}

func TestAuthenticated_wrongOrDuplicateToken_rejected(t *testing.T) {
	wrong := metadata.NewIncomingContext(
		context.Background(),
		metadata.Pairs(authHeader, "wrong-token"),
	)
	if authenticated(wrong, "expected-token") {
		t.Fatal("wrong authentication token was accepted")
	}
	duplicate := metadata.NewIncomingContext(
		context.Background(),
		metadata.Pairs(authHeader, "expected-token", authHeader, "expected-token"),
	)
	if authenticated(duplicate, "expected-token") {
		t.Fatal("duplicate authentication tokens were accepted")
	}
}

func TestRedactError_proxyCredentials_removed(t *testing.T) {
	got := redactError(assertionError("proxy socks5://user:secret@127.0.0.1 failed"))
	if got != "proxy socks5://***@127.0.0.1 failed" {
		t.Fatalf("unexpected redacted error: %q", got)
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }
