package wire

// RoutingMode is the RouterAgent wire-level routing decision type.
type RoutingMode uint8

const (
	RoutingModeAny RoutingMode = iota
	RoutingModeDirect
	RoutingModeHash
	RoutingModeBroadcast
)
