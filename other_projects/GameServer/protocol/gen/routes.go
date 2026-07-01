package rpc

// RouteEntry 描述一条已生成的路由
type RouteEntry struct {
	ServerType uint32
	Route string
	RspCmdID uint32
}

// RouteTable 是客户端入口路由表
var RouteTable = map[uint32]RouteEntry{
	2050: {ServerType: serverTypeLobby, Route: "LobbyHandler/ClaimReward", RspCmdID: 2051},
	2052: {ServerType: serverTypeLobby, Route: "LobbyHandler/SyncPos", RspCmdID: 0},
	2100: {ServerType: serverTypeRoom, Route: "RoomHandler/JoinRoom", RspCmdID: 2101},
	2102: {ServerType: serverTypeRoom, Route: "RoomHandler/LeaveRoom", RspCmdID: 0},
	2200: {ServerType: serverTypeMatch, Route: "MatchHandler/StartMatch", RspCmdID: 2201},
	2202: {ServerType: serverTypeMatch, Route: "MatchHandler/CancelMatch", RspCmdID: 0},
	2300: {ServerType: serverTypeOnline, Route: "OnlineHandler/QueryOnline", RspCmdID: 2301},
	2302: {ServerType: serverTypeOnline, Route: "OnlineHandler/Ping", RspCmdID: 0},
}

// AuthWhitelist 表示免鉴权的 CmdID 集合
var AuthWhitelist = map[uint32]bool{
	2052: true,
	2102: true,
	2302: true,
}
