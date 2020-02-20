package client

import (
	"context"
	"io"

	cid "github.com/ipfs/go-cid"
	pb "github.com/textileio/fil-tools/fps/pb"
)

type FPS struct {
	client pb.APIClient
}

func (f *FPS) Info(ctx context.Context) (*pb.InfoReply, error) {
	return f.client.Info(ctx, &pb.InfoRequest{})
}

func (f *FPS) Show(ctx context.Context, c cid.Cid) (*pb.ShowReply, error) {
	return f.client.Show(ctx, &pb.ShowRequest{
		Cid: c.String(),
	})
}

func (f *FPS) Store(ctx context.Context, c cid.Cid) error {
	_, err := f.client.Store(ctx, &pb.StoreRequest{
		Cid: c.String(),
	})
	if err != nil {
		return err
	}
	return nil
}

func (f *FPS) Get(ctx context.Context, c cid.Cid) (io.Reader, error) {
	stream, err := f.client.Get(ctx, &pb.GetRequest{
		Cid: c.String(),
	})
	if err != nil {
		return nil, err
	}
	reader, writer := io.Pipe()
	go func() {
		for {
			reply, err := stream.Recv()
			if err == io.EOF {
				_ = writer.Close()
				break
			} else if err != nil {
				_ = writer.CloseWithError(err)
				break
			}
			_, err = writer.Write(reply.GetChunk())
			if err != nil {
				_ = writer.CloseWithError(err)
				break
			}
		}
	}()

	return reader, nil
}

func (f *FPS) Create(ctx context.Context) (string, string, error) {
	r, err := f.client.Create(ctx, &pb.CreateRequest{})
	if err != nil {
		return "", "", err
	}
	return r.GetId(), r.GetAddress(), nil
}