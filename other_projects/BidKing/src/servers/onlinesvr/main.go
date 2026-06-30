package main

import (
	"fmt"
	"time"

	conf "project/conf/schema/gen"
	cfgloader "project/src/common/config"
	"project/src/common/logger"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cli"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/onlinesvr/internal"
)

var GitRevision = "dev"

func main() {
	cli.New("onlinesvr", "在线目录服务").
		DefaultConf("run/onlinesvr/conf/config.yaml").
		GitRevision(GitRevision).
		OnStart(runServer).
		Execute()
}

func runServer(f *cli.Flags) error {
	commonCfg, err := cfgloader.LoadCommon("run/common/conf/config.yaml")
	if err != nil {
		return err
	}

	svrLoader := cfgloader.NewLoader[conf.OnlineSvr](f.ConfFile)
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

	mod := internal.NewOnlineModule(internal.DefaultEntryTTL)
	app.Register(mod)
	if err := app.RegisterHandler(internal.NewOnlineHandler(mod.Directory(), app.Cluster()), nil); err != nil {
		return err
	}

	app.Start()
	if err := cls.Init(); err != nil {
		return err
	}
	defer cls.Stop()

	stop := svrLoader.Watch(30 * time.Second)
	defer stop()

	logger.Info("onlinesvr started", logger.String("nodeID", cfg.NodeId))
	app.Run()
	return nil
}
