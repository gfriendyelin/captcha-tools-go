代码fork自[kafuneri/captcha-tools](https://github.com/kafuneri/captcha-tools/)进行修改，感谢大佬的付出

# captcha-tools-go

这是 captcha-tools-main 的 Go + ONNX Runtime 迁移版。保留三组原始模型、Geetest 获取/提交流程、现有 HTTP API，以及模型按需加载和闲置卸载。原 Python 目录和容器不由本项目修改或替换。

## 模型与功能

| 模型组 | 原始文件 | 业务 |
| --- | --- | --- |
| PDL / 九宫格 | PP-HGNetV2-B4.onnx | V3 固定切图、分类匹配；保留旧余弦相似度分支 |
| D-FINE | d-fine-n.onnx | 点选检测、分类别 NMS、顺序与中心点 |
| Multi | yolo11n.onnx、dinov3-small.onnx、atten.onnx | YOLO 定位、DINOv3 特征、atten 匹配 |

五个模型原样复制，未量化、未替换。JPEG 使用与 Pillow 基线相同的 libjpeg-turbo 3.1.4.1；不包含 Python、NumPy 或 OpenCV。

## Docker 构建与部署

### linux/amd64

```bash
cd captcha-tools-go
docker build --pull -t captcha-tools-go:local .
docker compose up -d
```

Compose 创建新容器 captcha-tools-go，默认映射 19645:9645，不使用或替换 test-nine。容器内仍监听 9645。

```bash
curl 'http://127.0.0.1:19645/healthz'
curl 'http://127.0.0.1:19645/list_model'
curl 'http://127.0.0.1:19645/list_all_model'
```

如宿主机 9645 确认空闲，可自行把 Compose 端口改为 9645:9645；不要在原服务仍占用时直接修改并启动。

不使用 Compose 时：

```bash
docker run -d --name captcha-tools-go --restart unless-stopped   -p 19645:9645   -e MODEL_PRELOAD=false -e MODEL_IDLE_TIMEOUT=10m   -e use_pdl=1 -e use_dfine=1 -e use_multi=1   captcha-tools-go:local
```

### linux/arm64 与双架构

Dockerfile 使用 BuildKit TARGETARCH，分别下载并校验 ONNX Runtime 1.31.0 和 libjpeg-turbo 3.1.4.1 的原生库。

```bash
docker buildx build --platform linux/arm64 -t captcha-tools-go:arm64 --load .
docker buildx build --platform linux/amd64,linux/arm64   -t <你的仓库>/captcha-tools-go:latest --push .
```

CGo 构建阶段随目标平台运行；非本机构建需要 buildx/QEMU 或对应架构构建节点。最终镜像不包含 Go 编译器、构建工具、Python、NumPy、OpenCV、Matplotlib。

## GitHub Actions 自动构建

仓库内的 .github/workflows/docker.yml 会自动执行：

1. 在 Linux Docker 构建环境运行 go test ./...。
2. 构建并启动 amd64 最终镜像，检查健康接口以及真实 PDL 加载/卸载。
3. 使用 QEMU + Buildx 构建 linux/amd64、linux/arm64 多架构镜像。
4. main/master 分支、v* 标签推送，以及手动允许推送时，将镜像发布到 GHCR；Pull Request 不推送。

上传整个项目（包括隐藏的 .github 目录）后，推送 main 即可运行：

```bash
git init
git add .
git commit -m "Build Go captcha service"
git branch -M main
git remote add origin https://github.com/<owner>/<repo>.git
git push -u origin main
```

也可以在 GitHub 的 Actions → Docker CI → Run workflow 手动触发；取消 push_image 可只构建不发布。无需额外创建令牌，工作流使用 GITHUB_TOKEN 和 packages: write。

镜像地址：

```text
ghcr.io/<owner>/<repo>:latest
ghcr.io/<owner>/<repo>:sha-<commit>
```

GHCR 包首次可能是私有的，可在 GitHub Packages 的包设置中改为 Public，或使用具有 read:packages 权限的令牌登录后拉取。仓库名和镜像路径应使用小写。

拉取并运行（不会操作原 test-nine）：

```bash
docker pull ghcr.io/<owner>/<repo>:latest
docker run -d --name captcha-tools-go --restart unless-stopped   -p 19645:9645   -e MODEL_PRELOAD=false -e MODEL_IDLE_TIMEOUT=10m   ghcr.io/<owner>/<repo>:latest
```

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| PORT | 9645 | 进程监听端口 |
| use_pdl / use_dfine / use_multi | 1 | 是否启用对应组；也接受大写形式 |
| MODEL_PRELOAD | false | true/1 预加载全部启用组；也可设为 pdl,dfine,multi |
| MODEL_IDLE_TIMEOUT | 10m | Go duration；无租约且闲置到期后卸载；0 禁用自动卸载 |
| LOG_LEVEL | INFO | DEBUG/INFO/WARN/WARNING/ERROR |
| DINO_RANDOM_OFFSET | 1 | Multi 原版随机点击偏移；离线对照设为 0 |
| MODEL_DIR | model | 固定模型文件目录 |
| WORK_DIR | img_2_val | 每请求独立临时目录的父目录 |
| SAVE_DIR | img_saved | save_result=true 的持久化目录 |
| LOG_DIR | logs | 每日日志及最多两个历史日志 |
| HTTP_TIMEOUT | 30s | 每次上游 HTTP 调用超时 |
| MAX_IMAGE_BYTES | 33554432 | 上游响应体/图片大小上限 |
| ONNXRUNTIME_SHARED_LIBRARY_PATH | 镜像内 /usr/lib/libonnxruntime.so.1.31.0 | 原生库路径 |

MODEL_PRELOAD=false 时启动不加载模型。第一次请求只加载所需组；并发请求共享一次加载；租约未结束时不会卸载。再次请求自动重载。Multi 三个 Session 作为一个资源组加载和释放。

## API 兼容

所有原有路由均为 GET：

| 路由 | 参数与行为 |
| --- | --- |
| /pass_uni | 必填 gt、challenge；可选 save_result=false |
| /pass_nine | 另支持 use_v3_model=true、point=<float> |
| /pass_icon | gt、challenge、save_result |
| /pass_hutao | 保持 code/data/validate 结构与原 HTTP 状态 |
| /list_model | 当前已加载模型字符串数组 |
| /list_all_model | 所有模型组及关键字字符串数组 |
| /load_model?name=pdl | 支持原别名；之后仍受闲置超时控制 |
| /unload_model?name=multi | 等待在途推理并卸载整组 |
| /set_log_level?level=DEBUG | 动态设置日志级别 |

另提供 /healthz 供容器健康检查。普通验证接口直接返回上游 verify JSON；业务异常保持 {"error":"An internal server error occurred.","detail":"..."} 和 HTTP 500。缺少必填参数返回 422，非法日志级别返回 400。

支持原关键字：pdl/paddle/nine/原神/genshin、dfine/click/memo/note/便笺、multi/dino/click2/星穹铁道/崩铁/绝区零/zzz/hkrpg 及原 Session 名。Multi 的单个 Session 名会有意映射为整组安全卸载。

客户端调用逻辑不变，仅需按部署地址修改 endpoint，例如：

```text
http://127.0.0.1:19645/pass_uni?gt=...&challenge=...
```

## 并发、取消与保存

- 每次请求使用独立 WORK_DIR/request-<随机ID>，结束后清理，不共用 img_2_val 文件。
- 服务端图片名经 basename 和非法字符检查。
- 每次协议尝试有独立 Transport、CookieJar、HTTP/2、超时、context、响应体大小限制及关闭。
- ONNX Run 使用 RunOptions.Terminate 响应 context 取消。
- save_result=true 保存原图、九宫格切图或点选标注图；目标目录/同名文件带请求 ID并排他创建，避免并发覆盖。
- Session、输入/输出 Tensor、RunOptions 显式释放；卸载不依赖进程重启。

## 本地构建与测试

需要 Go 1.24+、CGo C 编译器、ONNX Runtime 1.31.0，以及 libjpeg-turbo 3.1.4.1 开发文件。若库安装在 /opt/libjpeg-turbo：

```bash
export CGO_CFLAGS='-I/opt/libjpeg-turbo/include'
export CGO_LDFLAGS='-L/opt/libjpeg-turbo/lib64'
export LD_LIBRARY_PATH='/opt/libjpeg-turbo/lib64'
gofmt -w ./cmd ./internal
go vet ./...
go test ./...
go test -race ./...
go build -trimpath -o captcha-tools ./cmd/captcha-tools
```

Windows 可把 onnxruntime.dll、onnxruntime_providers_shared.dll 放在 .onnxruntime/，并把 libjpeg-62.dll 所在 bin 目录加入 PATH。

真实 ONNX 生命周期测试：

```bash
CAPTCHA_INTEGRATION=1 go test -run TestRealONNX -v ./internal/inference
```

### Python 基线和 Go 对照

testdata/requirements-baseline.txt 固定 Python 3.12 基线依赖。导出工具只读取原源码/样本，临时切图在 Go 项目 testdata 下创建。

```bash
python testdata/export_python_baseline.py --source ../captcha-tools-main   --images ../captcha-tools-main/img_saved --output testdata/baseline-pdl.json --group pdl
python testdata/export_python_baseline.py --source ../captcha-tools-main   --images ../captcha-tools-main/img_saved --output testdata/baseline-dfine.json --group dfine
python testdata/export_python_baseline.py --source ../captcha-tools-main   --images ../captcha-tools-main/img_saved --output testdata/baseline-multi.json --group multi
```

Go 对照：

```bash
go run ./cmd/offlinecompare -group pdl -baseline testdata/baseline-pdl.json   -images ../captcha-tools-main/img_saved -output testdata/report-pdl.json
go run ./cmd/offlinecompare -group dfine -baseline testdata/baseline-dfine.json   -images ../captcha-tools-main/img_saved -output testdata/report-dfine.json
go run ./cmd/offlinecompare -group multi -baseline testdata/baseline-multi.json   -images ../captcha-tools-main/img_saved -output testdata/report-multi.json
```

PDL 坐标集合必须完全一致，旧分支顺序一致；点选中心误差上限 2 像素；Multi 关闭随机偏移并比较 YOLO 框和匹配顺序。逐样本 JSON 报告不会因少量浮点误差直接失败，也不会跳过结果。

### 反复卸载/重载与内存采样

```bash
go run ./cmd/stress -cycles 12 -output testdata/memory-report.json
go run ./cmd/stress -only multi -cycles 50 -output testdata/memory-report-multi50.json
```

报告包含每组首次调用、平均/最大重载调用耗时、逐轮 RSS、Go HeapAlloc/HeapSys。RSS 趋势单独记录，不会仅凭增长声称泄漏，也不会承诺卸载后立即等幅下降。

## 内存与已知限制

- 卸载销毁 ONNX Session 和本轮 Tensor，但 ONNX Runtime 全局环境保留到进程退出。arena、线程池、C/C++ 分配器和 Go 堆可保留内存，因此 RSS 不保证立即或等幅下降。
- 原包没有 resnet18.onnx 或转换所需 PTH。use_v3_model=false 与原服务一致，仍使用 PP-HGNet 输出做余弦相似度；没有伪造 ResNet 支持。
- 原包现有 img_saved 样本是同一批九宫格 JPG；D-FINE/Multi 对照验证数值流程，但不能替代真实线上点选图片的准确率评估。
- 测试不访问真实 Geetest；线上有效性、容器资源对比和长时间稳定性需部署后继续验证。
- 绘图使用内置位图字体，与 Pillow 默认字体外观不同；识别坐标不依赖该字体。
- 实际测试和未完成项见 VALIDATION.md。镜像 300～450MB 是目标，不是未构建前的事实。
