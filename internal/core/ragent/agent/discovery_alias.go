package agent

import "project/internal/core/ragent/agent/discovery"

type NodeInfo = discovery.NodeInfo

type MemberTable = discovery.MemberTable

type Resolver = discovery.Resolver

type Registry = discovery.Registry

var (
	NewMemberTable = discovery.NewMemberTable
	NewResolver    = discovery.NewResolver
)
