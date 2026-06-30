在Docker中运行集群，需要核心文件：
基础文件
docker-compose.yml — 最核心的文件，定义多个容器服务、网络和卷
Dockerfile — 自定义镜像构建文件（如果用官方镜像可省略）
.env — 环境变量配置文件