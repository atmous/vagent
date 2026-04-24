# vagent

给哪吒面板用的虚拟探针，当前按 Docker 使用。

第一次启动时，如果 `/config/config.yml` 不存在，会自动生成一个最小配置并直接使用。

## 快速运行

```bash
docker run -d \
  --name vagent \
  --restart unless-stopped \
  -e NZ_CLIENT_SECRET='replace_me' \
  -e NZ_SERVER='dashboard.example.com:443' \
  -v vagent-data:/config \
  niudea/vagent:latest
```

自动生成的配置内容如下：

```yaml
Linux:
  client_secret: "${NZ_CLIENT_SECRET}"
  server: "${NZ_SERVER}"
  tls: true
```

## 使用自己的配置

如果你已经有自己的配置文件，直接挂载到 `/config/config.yml`：

```bash
docker run -d \
  --name vagent \
  --restart unless-stopped \
  -e NZ_CLIENT_SECRET='replace_me' \
  -e NZ_SERVER='dashboard.example.com:443' \
  -v "$PWD/config.yml:/config/config.yml:ro" \
  niudea/vagent:latest
```

## 配置示例

配置是顶层实例结构，每个顶层 key 都是一台节点。完整示例见 [config.example.yml](/opt/codeserver/project/vagent/config.example.yml)。

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

- 每台节点至少要有 `client_secret`、`server` 和 `tls`。
- `host` 里只支持 `platform`、`platform_version`、`arch`、`cpu`、`gpu`、`mem_total`、`disk_total`、`swap_total`、`ipv4`。
- `host.mem_total`、`host.disk_total`、`host.swap_total` 必须带 `GB` 单位，比如 `4GB`、`40GB`、`2GB`。
- `host.cpu` 和 `host.gpu` 都是字符串数组。
- `host.ipv4` 是可选的，只在你想让面板显示 IPv4 归属地时才需要。
- 不要写 `uuid`、`boot_time`、`metrics`、`report_delay`、`virtualization` 这些字段，程序不读取。
