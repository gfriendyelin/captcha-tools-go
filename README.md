# captcha-tools-go

Geetest 点选验证码的 Go 实现。服务由两部分组成：

- **gateway**：常驻的轻量 HTTP 入口，不加载 ONNX 模型。
- **worker**：真正加载模型、执行验证码识别的进程；V3 和 V4 各自独立。

一个容器内可以同时运行 V3、V4 两个 worker，也可以拆成两个容器。worker 在 `WORKER_IDLE_TIMEOUT` 时间内没有请求会自行退出，由操作系统回收全部模型内存；下一次请求到来时 gateway 自动拉起新的 worker。

## 1. 架构

```text
:9645
  captcha-gateway            常驻，无模型
    ├── captcha-tools -worker (MODE=v3)
    │     ├── 默认预加载 PP-HGNetV2-B4.onnx + d-fine-n.onnx
    │     ├── 按需加载 Multi / Grid
    │     └── 空闲 1 分钟退出
    └── captcha-tools -worker (MODE=v4)
          ├── 默认不加载
          ├── 按需加载 Grid / Direction / Symbols / Word / Phrase
          └── 空闲 1 分钟退出
```

- V3 worker 在容器启动时拉起并预加载 `pdl`、`dfine`；V4 worker 在第一个 V4 请求到来时才拉起。
- 两个 worker 的闲置计时互相独立。V3 空闲只退出 V3，V4 不受影响。
- gateway 的 `/healthz` 永远在本地响应，不会因为 Docker healthcheck 而不断唤醒 worker。
- `MODE=all` 是单容器默认值；`MODE=v3` / `MODE=v4` 用于拆成两个容器。

## 2. Docker 部署

已构建镜像：`ghcr.io/gfriendyelin/captcha-tools-go:latest`。公开镜像无需登录即可拉取；如果 GHCR 仓库设为私有，需要先使用 GitHub PAT 执行 `docker login ghcr.io`。

### 2.1 单容器部署（推荐）

无需准备源码。以下命令同时启用 V3 和 V4，映射到宿主机 `19645` 端口：

```bash
docker pull ghcr.io/gfriendyelin/captcha-tools-go:latest

docker run -d \
  --name captcha-all \
  --restart unless-stopped \
  -p 19645:9645 \
  -e MODE=all \
  -e WORKER_IDLE_TIMEOUT=1m \
  -e WORK_DIR=/app/img_2_val \
  -e SAVE_DIR=/app/img_saved \
  -e LOG_LEVEL=INFO \
  -v captcha-saved:/app/img_saved \
  -v captcha-logs:/app/logs \
  --tmpfs /app/img_2_val:rw,noexec,nosuid,size=512m \
  ghcr.io/gfriendyelin/captcha-tools-go:latest
```

如果镜像没有内置模型，在 `docker run` 的镜像地址之前额外添加以下参数。`/opt/captcha/model` 请替换为宿主机模型目录的绝对路径，目录结构见第 4 节：

```bash
  -v /opt/captcha/model:/app/model:ro \
  -e MODEL_DIR=/app/model \
```

检查容器和日志：

```bash
docker ps --filter name=captcha-all
docker logs --tail 100 captcha-all
curl --fail http://127.0.0.1:19645/healthz
```

统一调用示例：

```bash
# V3
curl -G http://127.0.0.1:19645/pass_uni \
  --data-urlencode version=3 \
  --data-urlencode gt=你的gt \
  --data-urlencode challenge=你的challenge

# V4
curl -G http://127.0.0.1:19645/pass_uni \
  --data-urlencode version=4 \
  --data-urlencode captcha_id=你的captcha_id
```

`GET /readyz` 可查看 worker 状态。V4 默认懒加载，因此 V4 尚未被调用时，`MODE=all` 下的 `/readyz` 可能返回 503；Docker 健康检查使用 `/healthz`，不要使用 `/readyz`。

更新到最新镜像：

```bash
docker pull ghcr.io/gfriendyelin/captcha-tools-go:latest
docker stop captcha-all
docker rm captcha-all
# 再执行上面的 docker run 命令；命名卷中的结果和日志会保留。
```

### 2.2 可选：两个容器

V3 使用 `19645`，V4 使用 `19646`。两个容器使用同一镜像，Docker 会共享镜像层，不会重复保存模型文件：

```bash
docker run -d \
  --name captcha-v3 \
  --restart unless-stopped \
  -p 19645:9645 \
  -e MODE=v3 \
  -e WORKER_IDLE_TIMEOUT=1m \
  -v captcha-v3-saved:/app/img_saved \
  -v captcha-v3-logs:/app/logs \
  --tmpfs /app/img_2_val:rw,noexec,nosuid,size=512m \
  ghcr.io/gfriendyelin/captcha-tools-go:latest

docker run -d \
  --name captcha-v4 \
  --restart unless-stopped \
  -p 19646:9645 \
  -e MODE=v4 \
  -e WORKER_IDLE_TIMEOUT=1m \
  -v captcha-v4-saved:/app/img_saved \
  -v captcha-v4-logs:/app/logs \
  --tmpfs /app/img_2_val:rw,noexec,nosuid,size=512m \
  ghcr.io/gfriendyelin/captcha-tools-go:latest
```

此时分别访问 `http://127.0.0.1:19645/pass_uni`（V3）和 `http://127.0.0.1:19646/pass_v4`（V4）。单容器和两个容器的主要区别是故障隔离：单容器共用一个内存上限，gateway 故障会同时影响 V3/V4；两个容器可以分别限制内存和重启。

### 2.3 可选：Docker Compose（源码构建）

项目自带的 `docker-compose.yml` 保留本地构建配置。需要准备完整源码，并按第 4 节把模型放到 `model/`：

```bash
docker compose up -d --build
docker compose ps
docker compose logs -f captcha-tools-go
```

停止服务：

```bash
docker compose down
```

如果只使用 GHCR 现成镜像、不需要从源码构建，直接使用第 2.1 或 2.2 节的 `docker run` 命令即可。

### 2.4 可选：从源码构建

构建前先把模型按第 4 节放到 `model/` 目录，Dockerfile 会把它复制进镜像：

```bash
docker build -t captcha-tools-go:local .
```

构建完成后，将上述 `docker run` 命令最后的镜像名替换为 `captcha-tools-go:local`；也可以按第 2.3 节使用 Docker Compose。

## 3. HTTP API

### 3.1 统一入口

```http
GET /pass_uni?version=3&gt=...&challenge=...
GET /pass_uni?version=4&captcha_id=...
```

- `version=3` 或 `version=v3`：V3。
- `version=4` 或 `version=v4`：V4。
- 不传 `version` 时：
  - 有 `challenge` → V3
  - 有 `captcha_id` → V4
  - 只有 `gt`，既没有 `challenge` 也没有 `captcha_id` → 400，要求显式传 `version`
- V3/V4 的响应体保持各自原来的结构，不做归一化。
- 旧入口继续可用：

| 路径 | 目标 |
| --- | --- |
| `/pass_nine`、`/pass_icon`、`/pass_hutao` | V3 worker |
| `/pass_v4` | V4 worker |

### 3.2 V3 参数

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `gt` | 是 | 极验 gt |
| `challenge` | 是 | 极验 challenge |
| `save_result` | 否 | 覆盖服务端默认 `SAVE_RESULT` |
| `use_v3_model` | 否 | 默认 1；置 0 使用旧的九宫格 PDL 回退路径 |
| `point` | 否 | 浮点点选参数，原样透传 |

V3 的 `pic_type` 由上游决定：九宫格走 `pdl`，必要时语义兜底加载 `grid`；`icon` 走 Multi（`yolo11n + dinov3-small + atten`）；`icon1` 走 D-FINE。

### 3.3 V4 参数

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `captcha_id` / `gt` | 是 | 极验 captcha_id，`gt` 为兼容别名 |
| `client_type` | 否 | 透传给 V4 `/load` |
| `risk_type` | 否 | 透传给 V4 `/load` |
| `lang` | 否 | 透传给 V4 `/load` |
| `voice` | 否 | 是否使用语音题回退 |
| `save_result` | 否 | 覆盖服务端默认 `SAVE_RESULT` |

### 3.4 管理接口

管理接口支持 `version=3|4`，不传时默认 V3：

| 路径 | 说明 |
| --- | --- |
| `/list_model?version=3` | 当前已加载的模型组 |
| `/list_model?version=4` | 同上 |
| `/list_model?version=all` | 返回 `{"v3": [...], "v4": [...]}` |
| `/list_all_model?version=3` | 当前模式下全部可用模型说明 |
| `/load_model?version=4&name=grid` | 拉起对应 worker 并加载模型组 |
| `/unload_model?version=4&name=grid` | 卸载模型组；worker 仍存活 |
| `/set_log_level?version=3&level=DEBUG` | 调整 worker 日志级别 |

worker 未运行时，`/list_model` 返回空数组，不会为了查询而拉起 worker。模型内存的最终释放以 worker 闲置退出为准；`/unload_model` 只卸载模型组，不保证 RSS 回落。

## 4. 模型目录

```text
model/
├── PP-HGNetV2-B4.onnx          V3 九宫格分类
├── d-fine-n.onnx               V3 icon1
├── yolo11n.onnx                V3 icon
├── dinov3-small.onnx           V3 icon
├── atten.onnx                  V3 icon
├── grid/
│   ├── grid-vision.onnx        V4 九宫格 / V3 语义兜底
│   └── grid-head.onnx
├── v4-direction/
│   └── icon-direction.onnx
├── v4-symbols/
│   ├── symbol-detect.onnx
│   └── symbol-embedding.onnx
├── v4-word/
│   ├── word-detect.onnx
│   └── text-recognize.onnx
├── v4-phrase-core/
│   ├── phrase-detect.onnx
│   ├── phrase-recognize.onnx
│   └── phrase-order.onnx
└── v4-phrase-lexicon/
    ├── phrase-dictionary.json
    ├── phrase-lexicon.txt
    └── phrase-tokens.json
```

V3 和 V4 共用 `grid-vision/grid-head`。拆分 worker 后，如果两边同时活跃，这两个模型会在两个进程中各加载一份；这是按进程隔离换来的代价。

## 5. 环境变量

### 5.1 运行模式与进程生命周期

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MODE` | `all` | `all`：一个容器同时管理 V3/V4；`v3`：只启用 V3；`v4`：只启用 V4 |
| `PORT` | `9645` | gateway 监听端口；worker 使用 Unix Socket，不监听 TCP |
| `WORKER_IDLE_TIMEOUT` | `1m` | worker 无请求多久后退出；`0` 表示不自动退出 |
| `WORKER_START_TIMEOUT` | `2m` | gateway 等待 worker 完成启动/预加载的最长时间 |
| `WORKER_REQUEST_TIMEOUT` | `3m` | gateway 转发单个请求的最长时间 |
| `SHUTDOWN_TIMEOUT` | `10s` | gateway/worker 优雅关闭超时 |
| `WORKER_SOCKET` | 空 | 内部使用，gateway 会自动为子进程设置 |
| `WORKER_BINARY` | 空 | gateway 启动 worker 的二进制路径；默认同目录 `captcha-tools` |
| `CAPTCHA_TOOLS_PORT` | 空 | `PORT` 的兼容别名 |

`MODEL_IDLE_TIMEOUT` 已废弃，仅作为 `WORKER_IDLE_TIMEOUT` 的兼容别名；模型组不会再在进程内自动卸载。

### 5.2 模型

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MODEL_DIR` | `model` | 模型根目录 |
| `MODEL_PRELOAD` | 按 mode 决定 | `MODE=v3`/V3 worker 默认 `pdl,dfine`；V4 worker 默认空；`true` 表示该 mode 的默认值；`false` 表示全部不预加载 |
| `use_pdl` | `1` | 是否启用 PDL/HGNet 组 |
| `use_dfine` | `1` | 是否启用 D-FINE 组 |
| `use_multi` | `1` | 是否启用 Multi（YOLO + DINO + atten）组 |
| `use_grid` | `1` | 是否启用 Grid 组；V4 九宫格需要 |
| `DINO_RANDOM_OFFSET` | `1` | 是否启用 DINO 的随机偏移，默认开启 |
| `ONNXRUNTIME_SHARED_LIBRARY_PATH` | 平台默认 | ONNX Runtime 动态库路径 |

`MODE=all` 时，`MODEL_PRELOAD` 会按模型名拆分给两个 worker：V3 只接收 V3 模型组，V4 只接收 V4 模型组；`grid` 两边都会接收。

可用组名：

- V3：`pdl`、`dfine`、`multi`、`grid`
- V4：`grid`、`direction`、`symbols`、`word`、`phrase`

### 5.3 存储、日志与网络

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `WORK_DIR` | `img_2_val` | 请求临时目录，建议使用 tmpfs |
| `SAVE_DIR` | `img_saved` | `save_result=true` 时的保存目录 |
| `SAVE_RESULT` | `false` | 请求未传 `save_result` 时的默认值 |
| `LOG_DIR` | `logs` | 日志目录；gateway 和 worker 共同写入 |
| `LOG_LEVEL` | `INFO` | `DEBUG` / `INFO` / `WARN` / `ERROR` |
| `HTTP_TIMEOUT` | `10s` | 访问极验上游的单次 HTTP 超时 |
| `MAX_IMAGE_BYTES` | `10485760` | 单张图片最大字节数（10 MiB） |
| `DISABLE_DELAYS` | `false` | 仅测试/调试用；关闭 4 秒提交等待和 V3 随机延迟 |

## 6. 内存行为

- V3 worker 启动时加载 `PP-HGNetV2-B4.onnx` 和 `d-fine-n.onnx`；其他模型被请求时才加载。
- V4 worker 首次被调用时才启动，并且不预加载任何模型。
- `WORKER_IDLE_TIMEOUT` 到期且没有在途请求时，worker 退出，模型内存由操作系统回收。gateway 只保留很小的常驻内存。
- 再次请求时 gateway 自动拉起 worker，因此空闲后的第一次请求会有冷启动时间。
- 如果 V3/V4 持续有请求，worker 会保持存活；这是有意的，避免频繁加载模型。
- 想验证内存回落，可以使用：

```bash
docker stats --no-stream captcha-all
docker exec captcha-all ps -ef
```

worker 退出后，`ps` 中只剩 gateway；`docker stats` 的 RSS 会明显下降。

## 7. 本地开发

```bash
go test ./...
go test -race ./...
go vet ./...
```

本地启动 gateway（worker 二进制放在同目录，或通过 `WORKER_BINARY` 指定）：

```bash
go build -o bin/captcha-tools ./cmd/captcha-tools
go build -o bin/captcha-gateway ./cmd/captcha-gateway
WORKER_BINARY=./bin/captcha-tools ./bin/captcha-gateway
```

`cmd/captcha-tools -worker` 由 gateway 自动调用，一般不需要手工启动。

## 8. 已知限制

- V4 `svg_seed` / `svg_icon` 目前只完成取题，识别会返回 `Unsupported puzzle: svg`。
- V4 `voice` 使用 Go 内置语音识别回退，不加载 ONNX 模型。
- V3 的 `icon` 与 `icon1` 映射保持参考项目行为：`icon` 走 Multi，`icon1` 走 D-FINE。
- 单容器内 V3/V4 共用一个内存上限；需要独立限制或独立重启时，请使用两个容器。
