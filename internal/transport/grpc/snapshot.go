package grpc

import (
	"context"
	"net"

	"github.com/contenox/runtime/internal/modeld/snapshotauth"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const snapshotProofHeader = "x-contenox-local-snapshot"

// WithLocalSnapshots enables durable reconnection for Runtime clients with
// access to the same local data root.
func WithLocalSnapshots(root string) ServeOption {
	return func(s *Server) { s.snapshotKey = snapshotauth.Open(root) }
}

func WithSnapshotProof(ctx context.Context, proof string) context.Context {
	if proof == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, snapshotProofHeader, proof)
}

func (s *Server) snapshotContext(ctx context.Context, namespace string) context.Context {
	proofs := metadata.ValueFromIncomingContext(ctx, snapshotProofHeader)
	if len(proofs) != 1 {
		return ctx
	}
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ctx
	}
	addr, ok := p.Addr.(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return ctx
	}
	ctx, verified := s.snapshotKey.Authenticate(ctx, proofs[0], namespace, s.instanceID)
	if verified && s.backend == "shared" {
		// Managed children run on loopback under this same owner's epoch.
		return WithSnapshotProof(ctx, proofs[0])
	}
	return ctx
}
