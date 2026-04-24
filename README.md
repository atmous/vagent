# vagent

极简哪吒虚拟探针，只做三件事：

- 连接面板
- 上报虚拟主机信息
- 持续上报平滑随机状态

默认只支持 `docker run`。

## 运行

首次启动时，如果 `/config/config.yml` 不存在，容器会自动生成一个最小可用配置文件，然后直接使用它启动。

```bash
docker run -d \
  --name vagent \
  --restart unless-stopped \
  -e NZ_CLIENT_SECRET='replace_me' \
  -e NZ_SERVER='dashboard.example.com:443' \
  -v vagent-data:/config \
  niuniuo/vagent:latest
```

## 自定义配置

如果你已经有自己的配置文件，直接挂载成 `/config/config.yml`：

```bash
docker run -d \
  --name vagent \
  --restart unless-stopped \
  -e NZ_CLIENT_SECRET='replace_me' \
  -e NZ_SERVER='dashboard.example.com:443' \
  -v $PWD/config.yml:/config/config.yml:ro \
  niuniuo/vagent:latest
```

自动生成的 `config.yml` 故意保持最小可用。

完整字段示例可参考 [config.example.yml](/opt/codeserver/project/vagent/config.example.yml)。

## 配置格式

配置是“顶层实例”结构，没有公共默认块，也没有 `agents` 列表。

- 每个顶层 key 都是一台独立的虚拟机实例，比如 `Linux`、`Windows`
- 每台最少写 `client_secret`、`server`、`tls`
- 如果你要连同一个面板多台机器，就写多个顶层实例并重复对应的 `client_secret`、`server`、`tls`

最小可用配置只需要 `client_secret`、`server`、`tls` 三个值。实例里额外只保留 `host.platform`、`host.platform_version`、`host.arch`、`host.cpu`、`host.gpu`、`host.mem_total`、`host.disk_total`、`host.swap_total`、`host.ipv4` 这些字段。

`config.example.yml` 里的多个实例会直接复用同一组 `${NZ_CLIENT_SECRET}`、`${NZ_SERVER}`。程序会在每个实例上分别解析一次，所以你新增 agent 也不需要再扩展一套 `..._A`、`..._B`、`..._WINDOWS` 之类的环境变量名。

`uuid`、`boot_time`、`virtualization`、`metrics`、`report_delay` 等字段都不再提供配置。程序会按实例名和已给出的硬件信息自动生成稳定随机值。

`host.cpu`、`host.gpu` 是字符串数组。单个值可以写成 `["AMD EPYC 7B12 64-Core Processor"]`，多个值可以直接逗号分隔，例如 `["NVIDIA GeForce RTX 4090", "NVIDIA GeForce RTX 4080 SUPER"]`。

`host.ipv4` 是可选项，用来给面板做 IPv4 归属地显示。不写的话就不上报归属地显示。

`host.mem_total`、`host.disk_total`、`host.swap_total` 必须写成带单位的 GB 字符串，例如 `4GB`、`40GB`、`2GB`。程序内部会自动换算成字节。

## 目录

- [app/](/opt/codeserver/project/vagent/app)：手写业务代码
- [nezha/types.go](/opt/codeserver/project/vagent/nezha/types.go)：协议消息类型
- [nezha/service.go](/opt/codeserver/project/vagent/nezha/service.go)：gRPC 服务接口
- [nezha/nezha.proto](/opt/codeserver/project/vagent/nezha/nezha.proto)：协议源文件

## 镜像

镜像是多阶段构建，最终层基于 `scratch`，只包含：

- `/vagent`
- CA 证书
- `/config` 卷

## 自动发布

仓库内置了 GitHub Actions 自动发布流程：[.github/workflows/docker-publish.yml](/opt/codeserver/project/vagent/.github/workflows/docker-publish.yml)。

- 每次 push 到 `main`，会自动构建并推送 Docker Hub
- 默认会推两个 tag：`latest` 和当天日期，例如 `20260423`
- 发布的是多架构镜像清单，当前包含：`linux/amd64`、`linux/arm64`、`linux/arm/v7`、`linux/386`、`linux/ppc64le`、`linux/s390x`、`linux/riscv64`、`linux/loong64`

启用前需要在 GitHub 仓库里添加两个 Actions Secret：

- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

当前工作流会推送到 `${DOCKERHUB_USERNAME}/vagent`。如果以后你要改 Docker Hub 仓库名，不只是改用户名，而是连仓库名也要改，再调整工作流里的 `IMAGE_NAME` 即可。
