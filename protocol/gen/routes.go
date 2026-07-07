package rpc

// RouteEntry 描述一条已生成的路由
type RouteEntry struct {
	ServerType uint32
	Route      string
	RspCmdID   uint32
}

const (
	RouteLobbyHandlerPing = "LobbyHandler/Ping"
)

// RouteTable 是客户端入口路由表
var RouteTable = map[uint32]RouteEntry{
	2054: {ServerType: serverTypeLobby, Route: RouteLobbyHandlerPing, RspCmdID: 2055},
}

// AuthWhitelist 表示免鉴权的 CmdID 集合
var AuthWhitelist = map[uint32]bool{
	2054: true,
}
