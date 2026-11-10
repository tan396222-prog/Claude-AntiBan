// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package hostagent

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/lima-vm/lima/v2/pkg/guestagent/api"
	guestagentclient "github.com/lima-vm/lima/v2/pkg/guestagent/api/client"
)

type deadlineGuestService struct {
	api.UnimplementedGuestServiceServer
	deadlines chan time.Duration
}

func (s *deadlineGuestService) GetInfo(ctx context.Context, _ *emptypb.Empty) (*api.Info, error) {
	remaining := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		remaining = time.Until(deadline)
	}
	s.deadlines <- remaining
	return &api.Info{}, nil
}

func TestGuestAgentAvailabilityHasDeadline(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(grpc.Creds(guestagentclient.NewCredentials()))
	service := &deadlineGuestService{deadlines: make(chan time.Duration, 1)}
	api.RegisterGuestServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	client, err := guestagentclient.NewGuestAgentClient(listener.DialContext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if !isGuestAgentSocketAccessible(t.Context(), client) {
		t.Fatal("healthy guest was rejected")
	}
	if remaining := <-service.deadlines; remaining <= 0 || remaining > 5*time.Second {
		t.Fatalf("availability RPC must release the client lock within five seconds, deadline=%s", remaining)
	}
}
