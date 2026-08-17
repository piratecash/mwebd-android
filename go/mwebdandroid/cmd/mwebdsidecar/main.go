package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/ltcmweb/mwebd"
	"github.com/piratecash/mwebd-android/go/mwebdandroid"
	"github.com/piratecash/mwebd-android/go/mwebdandroid/internal/sidecar"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const authHeader = "x-mwebd-token"

var credentialURL = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^@\s]+@`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, redactError(err))
		os.Exit(1)
	}
}

func run() error {
	init, err := sidecar.ReadInit(os.Stdin)
	if err != nil {
		return fmt.Errorf("invalid initialization frame: %w", err)
	}
	if init.Mode == sidecar.ModeAddresses {
		return runAddresses(init)
	}
	return runDaemon(init)
}

func runAddresses(init *sidecar.Init) error {
	if init.FromIndex < 0 || init.ToIndex < init.FromIndex {
		return errors.New("invalid address index range")
	}
	if len(init.ScanSecret) != 32 {
		return errors.New("invalid scan secret length")
	}
	if len(init.SpendPublicKey) == 0 {
		return errors.New("spend public key is empty")
	}
	encoded := mwebd.Addresses(init.ScanSecret, init.SpendPublicKey, init.FromIndex, init.ToIndex)
	addresses := []string{}
	if encoded != "" {
		addresses = strings.Split(encoded, ",")
	}
	return sidecar.WriteAddresses(os.Stdout, addresses)
}

func runDaemon(init *sidecar.Init) error {
	if len(init.Token) < 32 {
		return errors.New("authentication token is too short")
	}
	if init.DataDir == "" {
		return errors.New("data directory is empty")
	}
	if err := mwebdandroid.BootstrapRestoreCheckpoint(init.DataDir, init.RestoreCheckpoint); err != nil {
		return fmt.Errorf("restore checkpoint failed: %w", err)
	}

	server, err := mwebd.NewServer2(&mwebd.ServerArgs{
		Chain:              init.Chain,
		DataDir:            init.DataDir,
		PeerAddr:           init.PeerAddress,
		ProxyAddr:          init.ProxyAddress,
		UnaryInterceptors:  []grpc.UnaryServerInterceptor{unaryAuthenticator(init.Token)},
		StreamInterceptors: []grpc.StreamServerInterceptor{streamAuthenticator(init.Token)},
	})
	if err != nil {
		return fmt.Errorf("daemon initialization failed: %w", err)
	}
	port, err := server.Start(0)
	if err != nil {
		_ = server.Stop()
		return fmt.Errorf("daemon listener failed: %w", err)
	}
	if err = sidecar.WriteReady(os.Stdout, sidecar.Ready{
		ProtocolVersion: sidecar.ProtocolVersion,
		NativeVersion:   mwebdandroid.Version(),
		Port:            uint32(port),
	}); err != nil {
		_ = server.Stop()
		return fmt.Errorf("startup frame failed: %w", err)
	}

	parentClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(parentClosed)
	}()
	serverStopped := make(chan error, 1)
	go func() {
		serverStopped <- server.Wait()
	}()
	signalContext, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-parentClosed:
	case <-signalContext.Done():
	case err = <-serverStopped:
		return err
	}
	return server.Stop()
}

func unaryAuthenticator(token string) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if !authenticated(ctx, token) {
			return nil, status.Error(codes.Unauthenticated, "invalid mwebd token")
		}
		return handler(ctx, req)
	}
}

func streamAuthenticator(token string) grpc.StreamServerInterceptor {
	return func(
		srv any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if !authenticated(stream.Context(), token) {
			return status.Error(codes.Unauthenticated, "invalid mwebd token")
		}
		return handler(srv, stream)
	}
}

func authenticated(ctx context.Context, expected string) bool {
	values := metadata.ValueFromIncomingContext(ctx, authHeader)
	if len(values) != 1 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(values[0]), []byte(expected)) == 1
}

func redactError(err error) string {
	return credentialURL.ReplaceAllString(err.Error(), "${1}***@")
}
