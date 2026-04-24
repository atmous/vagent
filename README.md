# vagent

`vagent` 是一个给哪吒面板用的虚拟探针。

它不采集真实机器状态，也不打算把自己伪装成完整系统代理。它做的事情很简单：连上面板，提交一份你定义好的主机信息，然后持续上报一组稳定、平滑、看起来像真实机器的状态数据。

如果你只是想在面板里放几台“长期在线、展示稳定”的虚拟节点，它很合适。  
如果你需要真实硬件监控、进程信息、磁盘细节或者系统排障，这个项目不适合，直接用原版探针更省事。

## 快速开始

默认用 Docker 跑。第一次启动时，如果 `/config/config.yml` 不存在，容器会自动生成一个最小配置文件；你只需要把面板地址和 `client_secret` 通过环境变量传进去。

```bash
docker run -d \
  --name vagent \
  --restart unless-stopped \
  -e NZ_CLIENT_SECRET='replace_me' \
  -e NZ_SERVER='dashboard.example.com:443' \
  -v vagent-data:/config \
  niudea/vagent:latest
```

生成出来的默认配置很克制，只有这一份最小结构：

```yaml
Linux:
  client_secret: "${NZ_CLIENT_SECRET}"
  server: "${NZ_SERVER}"
  tls: true
```

## 自定义配置

如果你想定义多台虚拟机，或者给它们指定不同的平台、CPU、GPU、内存和磁盘信息，直接挂载自己的配置文件：

```bash
docker run -d \
  --name vagent \
  --restart unless-stopped \
  -e NZ_CLIENT_SECRET='replace_me' \
  -e NZ_SERVER='dashboard.example.com:443' \
  -v "$PWD/config.yml:/config/config.yml:ro" \
  niudea/vagent:latest
```

完整示例见 [config.example.yml](/opt/codeserver/project/vagent/config.example.yml)。

配置不是 `agents` 列表，而是“顶层实例”结构。每个顶层 key 都是一台独立节点，比如：

```yaml
Linux:
  client_secret: "${NZ_CLIENT_SECRET}"
  server: "${NZ_SERVER}"
  tls: true
  host:
    platform: Linux
    platform_version: "6.1"
    arch: amd64
    cpu: ["AMD EPYC 7B12 64-Core Processor"]
    mem_total: 4GB
    disk_total: 40GB

Windows:
  client_secret: "${NZ_CLIENT_SECRET}"
  server: "${NZ_SERVER}"
  tls: true
  host:
    platform: Windows
    platform_version: "Server 2022"
    arch: amd64
    gpu: ["NVIDIA GeForce RTX 4090"]
    mem_total: 8GB
    disk_total: 80GB
```

## 配置规则

有几个规则提前知道会省很多时间：

- 每台节点至少要有 `client_secret`、`server` 和 `tls`。
- `host` 里只支持 `platform`、`platform_version`、`arch`、`cpu`、`gpu`、`mem_total`、`disk_total`、`swap_total`、`ipv4`。
- `host.mem_total`、`host.disk_total`、`host.swap_total` 必须带 `GB` 单位，比如 `4GB`、`40GB`、`2GB`。
- `host.cpu` 和 `host.gpu` 都是字符串数组。
- `host.ipv4` 是可选的，只在你想让面板显示 IPv4 归属地时才需要。

项目刻意没有开放 `uuid`、`boot_time`、`metrics`、`report_delay`、`virtualization` 这些字段。它们会根据实例名、服务器地址和硬件信息自动生成。这样做的好处是，节点身份是稳定的，状态曲线也不会每次启动都像换了一台机器。

## 这个项目会帮你处理什么

- 自动生成稳定的 `uuid`
- 自动生成合理的开机时间
- 自动推断一组平滑的 CPU、内存、磁盘、网络、连接数和温度变化
- 有 GPU 时自动补对应的 GPU 占用和温度曲线
- 配了 `ipv4` 时定期上报归属地显示

这也是它和“纯静态假数据”最大的区别：面板里看到的不是死值，而是一条会动、但不乱跳的状态线。

## 镜像与发布

镜像用多阶段构建，最终运行层基于 `scratch`，里面只有二进制、CA 证书和 `/config` 卷，体积比较干净。Dockerfile 在 [Dockerfile](/opt/codeserver/project/vagent/Dockerfile)。

仓库内置了自动发布流程：[.github/workflows/docker-publish.yml](/opt/codeserver/project/vagent/.github/workflows/docker-publish.yml)。

- push 到 `main` 后会自动构建并推送 Docker Hub
- 默认发布 `latest` 和当天日期两个 tag
- 当前会推到 `${DOCKERHUB_USERNAME}/vagent`
- 需要预先配置两个 Actions Secret：`DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN`

## 项目结构

- [app/](/opt/codeserver/project/vagent/app)：配置解析、节点运行和状态生成逻辑
- [nezha/](/opt/codeserver/project/vagent/nezha)：哪吒协议相关类型和服务定义
- [config.example.yml](/opt/codeserver/project/vagent/config.example.yml)：多实例配置示例
