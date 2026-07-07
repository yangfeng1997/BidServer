package main

import (
	"fmt"
	"time"

	conf "project/conf/schema/gen"
	cfgloader "project/src/common/config"
	"project/src/common/logger"
	"project/src/common/mongo"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cli"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/lobbysvr/internal"
)

var GitRevision = "dev"

func main() {
	cli.New("lobbysvr", "大厅服务").
		DefaultConf("run/lobbysvr/conf/config.yaml").
		GitRevision(GitRevision).
		OnStart(runServer).
		Execute()
}

func runServer(f *cli.Flags) error {
	commonCfg, err := cfgloader.LoadCommon("run/common/conf/config.yaml")
	if err != nil {
		return err
	}

	svrLoader := cfgloader.NewLoader[conf.LobbySvr](f.ConfFile)
	svrLoader.MustLoad()
	cfg := svrLoader.Current()

	if f.Addr != "" {
		cfg.Addr = f.Addr
	}

	var log logger.Logger
	if f.LogFile != "" {
		var lc *logger.LogCloser
		log, lc, err = logger.NewZapLoggerFromFile(f.LogFile)
		if err != nil {
			return fmt.Errorf("init logger: %w", err)
		}
		defer lc.Close()
	} else {
		log, _ = logger.NewZapDevelopment()
	}
	logger.SetGlobal(log)

	self, err := cluster.ParseNodeID(cfg.NodeId)
	if err != nil {
		return err
	}
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints:  commonCfg.Etcd.Endpoints,
		NatsURLs:       commonCfg.Nats.Urls,
		SelfAddr:       cfg.Addr,
		ServerTypeName: cfg.ServerTypeName,
	})
	if err != nil {
		return err
	}

	app := application.NewBuilder().
		NodeID(cfg.NodeId).
		NodeType(cfg.ServerTypeName).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).
		Build()

	mc, err := mongo.Connect(commonCfg.Mongo.Uri, commonCfg.Mongo.Database, 10*time.Second)
	if err != nil {
		return err
	}
	defer mc.Close()

	rt := internal.NewRuntime(internal.RuntimeConfig{
		NodeID:       cfg.NodeId,
		Cluster:      app.Cluster(),
		Store:        internal.NewMongoStore(mc),
		MailStore:    internal.NewMongoMailStore(mc),
		OfflineStore: internal.NewMongoOfflineStore(mc),
	})

	app.Register(internal.NewLobbyModule(rt))
	if err := app.RegisterHandler(internal.NewLobbyHandler(rt), nil); err != nil {
		return err
	}

	app.Start()
	if err := cls.Init(); err != nil {
		return err
	}
	defer cls.Stop()

	stop := svrLoader.Watch(30 * time.Second)
	defer stop()

	logger.Info("lobbysvr started", logger.String("nodeID", cfg.NodeId))
	app.Run()
	return nil
}
