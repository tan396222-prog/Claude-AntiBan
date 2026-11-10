package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/lima-vm/lima/v2/pkg/guestagent"
	"github.com/lima-vm/lima/v2/pkg/guestagent/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

type streamingAgent struct{ guestagent.Agent }

func (*streamingAgent) Events(ctx context.Context, responses chan *api.Event) {
	defer close(responses)
	select {
	case responses <- &api.Event{}:
	case <-ctx.Done():
		return
	}
	<-ctx.Done()
}

func TestShutdownCancelsActiveStream(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- StartServer(ctx, listener, &GuestServer{Agent: &streamingAgent{}}) }()
	client, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	streamCtx, stopStream := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopStream()
	stream, err := api.NewGuestServiceClient(client).GetEvents(streamCtx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("guest server waited for a live stream after shutdown")
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("stream survived server shutdown")
	}
}
