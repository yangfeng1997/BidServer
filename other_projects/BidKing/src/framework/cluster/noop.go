package cluster

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
)

var errNoopCluster = errors.New("cluster not configured, use WithCluster()")

type noopCluster struct{}

var _ Cluster = (*noopCluster)(nil)

func (nc *noopCluster) Name() string { return "noop" }
func (nc *noopCluster) Init() error  { return nil }
func (nc *noopCluster) Stop() error  { return nil }

func (nc *noopCluster) Call(_ context.Context, _ NodeID, _ string, _ proto.Message, done func([]byte, error)) {
	done(nil, errNoopCluster)
}
func (nc *noopCluster) CallRaw(_ context.Context, _ NodeID, _ string, _ []byte, done func([]byte, error)) {
	done(nil, errNoopCluster)
}
func (nc *noopCluster) CallRawSync(_ context.Context, _ NodeID, _ string, _ []byte) ([]byte, error) {
	return nil, errNoopCluster
}
func (nc *noopCluster) CallSync(_ context.Context, _ NodeID, _ string, _ proto.Message) ([]byte, error) {
	return nil, errNoopCluster
}
func (nc *noopCluster) Cast(_ context.Context, _ NodeID, _ string, _ proto.Message) error {
	return errNoopCluster
}
func (nc *noopCluster) CastRaw(_ context.Context, _ NodeID, _ string, _ []byte) error {
	return errNoopCluster
}
func (nc *noopCluster) CallAny(_ context.Context, _ string, _ string, _ proto.Message, done func([]byte, error)) {
	done(nil, errNoopCluster)
}
func (nc *noopCluster) CallAnyRaw(_ context.Context, _ string, _ string, _ []byte, done func([]byte, error)) {
	done(nil, errNoopCluster)
}
func (nc *noopCluster) CallAnySync(_ context.Context, _ string, _ string, _ proto.Message) ([]byte, error) {
	return nil, errNoopCluster
}
func (nc *noopCluster) CastAny(_ context.Context, _ string, _ string, _ proto.Message) error {
	return errNoopCluster
}
func (nc *noopCluster) CastAnyRaw(_ context.Context, _ string, _ string, _ []byte) error {
	return errNoopCluster
}
func (nc *noopCluster) Broadcast(_ context.Context, _ string, _ string, _ proto.Message) {}

func NewNoopCluster() Cluster { return &noopCluster{} }
