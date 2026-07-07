package main

import (
	"fmt"
	"time"

	conf "project/conf/schema/gen"
	cfgloader "project/src/common/config"
	"project/src/common/logger"
	"project/src/common/matchqueue"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cli"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/routersvr/internal"
)

var GitRevision = "dev"

func main() {
	cli.New("routersvr", "路由代理服务").
		DefaultConf("run/routersvr/conf/config.yaml").
		GitRevision(GitRevision).
		OnStart(runServer).
		Execute()
}

func runServer(f *cli.Flags) error {
	commonCfg, err := cfgloader.LoadCommon("run/common/conf/config.yaml")
	if err != nil {
		return err
	}

	svrLoader := cfgloader.NewLoader[conf.RouterSvr](f.ConfFile)
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
		AsyncDispatch:  true,
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

	mq, err := matchqueue.NewJetStreamQueue(commonCfg.Nats.Urls)
	if err != nil {
		return err
	}
	defer mq.Close()

	mod := internal.NewRouterModule(cls.Discovery(), app.Cluster(), mq)
	app.Register(mod)
	if err := app.RegisterHandler(internal.NewRouterHandler(mod), nil); err != nil {
		return err
	}

	app.Start()
	if err := cls.Init(); err != nil {
		return err
	}
	defer cls.Stop()

	stop := svrLoader.Watch(30 * time.Second)
	defer stop()

	logger.Info("routersvr started", logger.String("nodeID", cfg.NodeId))
	app.Run()
	return nil
}
