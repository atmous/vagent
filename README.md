# vagent

给哪吒面板用的虚拟探针，当前按 Docker 使用。

使用时只读 `/config/config.yml`，不读取 `NZ_CLIENT_SECRET`、`NZ_SERVER` 这类环境变量。

## 运行

```bash
cat > config.yml <<'EOF'
Linux:
  client_secret: "replace_with_your_client_secret"
  server: "dashboard.example.com:443"
  tls: true
EOF

docker run -d \
  --name vagent \
  --restart unless-stopped \
  -v "$PWD/config.yml:/config/config.yml:ro" \
  niudea/vagent:latest
```

## 配置示例

```yaml
Linux:
  client_secret: "replace_with_your_client_secret"
  server: "dashboard.example.com:443"
  tls: true
  host:
    platform: Linux
    platform_version: "6.1"
    arch: amd64
    cpu: ["AMD EPYC 7B12 64-Core Processor"]
    mem_total: 4GB
    disk_total: 40GB

Windows:
  client_secret: "replace_with_your_client_secret"
  server: "dashboard.example.com:443"
  tls: true
  host:
    platform: Windows
    platform_version: "Server 2022"
    arch: amd64
    gpu: ["NVIDIA GeForce RTX 4090"]
    mem_total: 8GB
    disk_total: 80GB
```

完整示例见 [config.example.yml](/opt/codeserver/project/vagent/config.example.yml)。
