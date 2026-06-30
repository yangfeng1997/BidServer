package main

import (
	"fmt"
	"time"

	conf "project/conf/schema/gen"
	"project/protocal/gen/routes"
	cfgloader "project/src/common/config"
	"project/src/common/logger"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cli"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/gatesvr/internal"
)

var GitRevision = "dev"

func main() {
	cli.New("gatesvr", "网关服务").
		DefaultConf("run/gatesvr/conf/config.yaml").
		GitRevision(GitRevision).
		OnStart(runServer).
		Execute()
}

func runServer(f *cli.Flags) error {
	commonCfg, err := cfgloader.LoadCommon("run/common/conf/config.yaml")
	if err != nil {
		return err
	}

	svrLoader := cfgloader.NewLoader[conf.GateSvr](f.ConfFile)
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
		Frontend(cfg.Addr).
		Serializer("protobuf", protobuf.NewSerializer()).
		Routes(routes.Config()).
		Cluster(cls).
		Build()

	gateModule := internal.NewGateModule(cfg.NodeId, app.Sessions(), app.Cluster(), app.AgentMap())
	app.Register(gateModule)
	if err := app.RegisterHandler(internal.NewGateHandler(gateModule), nil); err != nil {
		return err
	}

	app.Start()
	if err := cls.Init(); err != nil {
		return err
	}
	defer cls.Stop()

	stop := svrLoader.Watch(30 * time.Second)
	defer stop()

	logger.Info("gatesvr started",
		logger.String("nodeID", cfg.NodeId),
		logger.String("addr", cfg.Addr))
	app.Run()
	return nil
}
