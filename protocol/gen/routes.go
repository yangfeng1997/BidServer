package rpc

// RouteEntry 描述一条已生成的路由
type RouteEntry struct {
	ServerType uint32
	Route      string
	RspCmdID   uint32
}

// RouteTable 是客户端入口路由表
var RouteTable = map[uint32]RouteEntry{
	2050: {ServerType: serverTypeLobby, Route: "LobbyHandler/ClaimReward", RspCmdID: 2051},
	2052: {ServerType: serverTypeLobby, Route: "LobbyHandler/SyncPos", RspCmdID: 0},
}

// AuthWhitelist 表示免鉴权的 CmdID 集合
var AuthWhitelist = map[uint32]bool{
	2052: true,
}
