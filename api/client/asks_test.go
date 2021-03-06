package client

import (
	"testing"

	"github.com/textileio/powergate/index/ask"
	pb "github.com/textileio/powergate/index/ask/pb"
)

func TestGetAsks(t *testing.T) {
	skipIfShort(t)
	a, done := setupAsks(t)
	defer done()

	_, err := a.Get(ctx)
	if err != nil {
		t.Fatalf("failed to call Get: %v", err)
	}
}

func TestQuery(t *testing.T) {
	skipIfShort(t)
	a, done := setupAsks(t)
	defer done()

	_, err := a.Query(ctx, ask.Query{MaxPrice: 5})
	if err != nil {
		t.Fatalf("failed to call Query: %v", err)
	}
}

func setupAsks(t *testing.T) (*Asks, func()) {
	serverDone := setupServer(t)
	conn, done := setupConnection(t)
	return &Asks{client: pb.NewAPIClient(conn)}, func() {
		done()
		serverDone()
	}
}
