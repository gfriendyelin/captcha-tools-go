# 交接任务书：继续完成 captcha-tools-go（Geetest 验证码求解器 Go 移植）

你接手的仓库位于 `captcha-tools-go/`（Go 语言，module `github.com/kafuneri/captcha-tools-go`，go 1.24）。
目标：把 TypeScript 版 `Lotus-Yunzai/packages/captcha`（位于 `E:\dsh work\Lotus-Yunzai`，注意不是 -main）的 test-nine 求解器代码与模型，**逐行忠实移植**为 Go 语言，并满足 V3 与 V4 完全独立、离线可用的验收标准。

## 一、已完成的工作（当前进度）

### 2026-11-26 追加进度

- **V4 全链编译通过**：本机已装 Go 1.24.13（便携版）；因无 C 编译器，cgo 依赖（onnxruntime_go、go-libjpeg）用替身模块做类型检查，`go build ./...`、`go vet ./...` 全绿；真实 `go.mod`/`go.sum` 也用 `-modfile` 校验过。镜像内（有 C 工具链）仍需再跑一次真实构建。
- **修复的编译/逻辑问题**：
  - `text_solvers.go`：字符串里裸换行（改为 `"\n"`）、缺少 `_ "embed"` 导入、三处函数间缺空行。
  - `vision/slide.go`：`TemplateMatchCCOEFFNormed` 未使用变量 `n`。
  - `inference/icon_solvers.go`：`vision.RotateRGBWhite(base, angle)` 需 `float64(angle)`。
  - `inference/predictor.go`：旧 PDL 分支未使用变量 `batch`。
  - `vision/rotate.go`：`rotate180` 对奇数行高错误（中间行未水平翻转），已改为逐像素反查。
- **单测修正**（原测试断言与 Lotus 语义不符，非实现错误）：letterbox 通道下标、YOLO box 的 float32 截断（71,31）、90CW/270 旋转方向、灰度高亮合成值、slide 模板匹配需有纹理的合成图。
- **BoardSolver 已移植**：`internal/board/board.go`（3x3 match / 5x5 gomoku，逐行对照 `solvers/board.ts`），`board_test.go` 覆盖横/纵获胜、五子棋、非法盘面、无解、输入不被修改。
- **VoiceSolver 已移植**：`internal/voice/voice.go`。MP3 解码采用纯 Go 依赖 `github.com/hajimehoshi/go-mp3 v0.3.4`（无 cgo），线性重采样到 16 kHz、FFT/Mel/DCT/MFCC、一阶二阶 delta、能量分段与模板匹配均按 `solvers/voice.ts` 逐行实现；模板 JSON 移到 `internal/voice/resources/` 并 go:embed（`inference/resources` 里未使用的两份已删除）。
- **测试结果**（替身模块环境）：`internal/board`、`internal/voice`、`internal/inference`、`internal/model`、`internal/server`、`internal/geetest`、`internal/config`、`internal/storage` 全部通过。`internal/vision` 仅 `TestLibJPEGMatchesPillowGolden`、`TestMemoryJPEGAndDrawing` 失败，原因是替身 libjpeg 无法解码 JPEG（真实镜像内应有 libjpeg-turbo，需在 Docker 或装好 C 工具链的机器上复跑）。
- gofmt：`gofmt -l ./cmd ./internal` 无输出。
- **`GET /pass_v4` 已按 V3 风格暴露**（2026-11-26）：
  - `internal/v4/`：V4 协议层。`crypto.go`（pt=0 base64url、pt=1 AES-CBC+RSA、pt=2 SM4-CBC+gmsm SM2 C1C2C3（去掉 0x04 前缀，与 sm-crypto 的 doEncrypt(...,0) 一致）、PoW、lotFields、HMAC）、`keyring.go`（decodeURI/keytable XOR 提取 RSA 模数并缓存）、`track.go`（Builder/sequence/click/grid/board/slide/gzip+base64url）、`client.go`（/load、/verify、JSONP、资源域名校验、patrol 配置、puzzle 构建、nine 九宫格裁剪、密钥失效恢复后由上层重试一次）。
  - release 配置嵌入：`internal/v4/resources/{v1.9.7,v1.9.8,v1.9.9}-config.json`（来自 Lotus）。
  - `internal/solver/v4.go`：按 registry 顺序分派 slide / grid(GridMatch) / click（direction → symbol-embedding / word / phrase）/ match+winlinze(board) / voice / silent。
  - server：`GET /pass_v4`（必填 `captcha_id`，兼容 `gt`；可选 `risk_type/lang/client_type/voice/save_result`），加载→4s 延迟→提交→原样返回上游 verify JSON；错误格式与 V3 相同。`storage.Request.CommitFiles` 负责通用资产落盘（第一张资产视为原图）。
  - `cmd/captcha-tools/main.go`：`v4FlowAdapter` 在 `ErrProtocolKeyUpdated` 时重新 /load + 重解 + 重提一次。
  - 测试：`internal/v4`（crypto/keyring/track/PoW/JSONP/load+verify mock）、`internal/server/server_v4_test.go`（成功、gt 别名、save_result、silent、422/500）。全部通过。
- **类型检查方式已简化**：仓库根新增 `typecheck.mod` / `typecheck.sum`，stub 在 `.typecheck/stubs/`。直接 `go build/vet/test '-modfile=typecheck.mod' ./...`（PowerShell 里 modfile 参数要加引号），无需再复制源码。`.typecheck/src/` 是旧副本且已过期，可删除；当前 Shell 沙箱无 E: 写权限，删除请手动执行。

### V3 链（pdl / dfine / multi / grid）—— 已完成，基线测试通过
- 服务端 :9645、配置、日志、模型池、ONNX Runtime 封装、轨迹、加密、JSON、存储、进程内存监控（RSS）均已就绪。
- 模型文件在 `captcha-tools-go/model/`：V3 平铺（PP-HGNetV2-B4、d-fine-n、yolo11n、dinov3-small、atten），V4 分包目录 `grid/`、`v4-direction/`、`v4-symbols/`、`v4-word/`、`v4-phrase-core/`、`v4-phrase-lexicon/`（来自 `E:\dsh work\model-packs`，单项均 <100MB）。`MODEL_DIR` 默认 `model`；`grid` 从 `model/grid` 加载。
- 金标基线：`testdata/baseline-*.json`、`report-*.json`、`memory-report*.json`、`jpeg_golden.json`。

### V4 链（slide / direction / symbols / word / phrase）—— 代码已移植并编译通过，待真实模型验证
| 求解器 | 文件 | 状态 |
|---|---|---|
| 滑块 Slide | `internal/vision/slide.go` + `internal/inference/slide.go` + `slide_test.go` | 已完成（合成图单测通过） |
| 方向图标 | `internal/inference/icon_solvers.go`（DirectionSolve，8 个硬编码 md5→方向）+ `internal/vision/detect.go` | 已完成 |
| 符号点选 | `internal/inference/icon_solvers.go`（SymbolRegionsSolve / SymbolEmbeddingSolve）+ `internal/vision/icon.go`、`rotate.go` | 已完成（**刻意不用** embedded-detect/dino，模型不在包内，只用 symbol-detect YOLOX） |
| 文字点选 Word | `internal/inference/text_solvers.go`（WordSolve） | 已编译验证 |
| 短语点选 Phrase | `internal/inference/text_solvers.go`（PhraseSolve） | 已编译验证 |

- `internal/inference/registry.go` 已加入 direction / symbols / word / phrase 四个组与别名（如 `direction`/方向/icon_direction、`symbols`/图标/icon、`word`/文字/字符、`phrase`/短语/短句/成语）。
- 数据文件已嵌入：`internal/inference/resources/` 下有 ocr-characters.json、legacy-voice.json、v4_voice_20261012-voice.json（通过 //go:embed 嵌入，见 text_solvers.go 顶部）。

## 二、剩余工作（按优先级）

1. **（已完成）编译验证 text_solvers.go + registry.go**
2. **（已完成）BoardSolver 移植**：`internal/board/`
3. **（已完成）VoiceSolver 移植**：`internal/voice/`，新增纯 Go MP3 解码依赖 go-mp3 v0.3.4（已写入 go.mod/go.sum）。
4. **（已完成）V4 协议适配器与 `/pass_v4` 路由**：`internal/v4/` + `internal/solver/` + server 路由。pt=2 已支持（新增纯 Go 依赖 `github.com/emmansun/gmsm v0.43.0`，SM4-CBC 用 `sm4CBCEncrypt`，SM2 固定公钥 + **C1C2C3** 去 0x04 前缀；`TestSM4CBCVectors` 用 OpenSSL 生成的 4 组向量、`TestEncryptPayloadMode2RoundTrip` 用临时 SM2 密钥对解密回环，均已通过）。剩余：svg_seed/svg_icon 只完成取题，求解器未移植；voice 回退（show_voice）未实现；真实环境需验证密钥恢复与 patrol 配置。
   - **pt=2 上线前验证记录（2026-11-26）**：用 sm-crypto@0.3.13 对 Go 产物做交叉解密，4 组 payload 的 SM2 封装均解出 16 字节 ASCII 密钥、SM4 解出原文，`PASSED`。由此发现并修正了拼接顺序：sm-crypto 的 `C1C2C3=0`、`C1C3C2=1`，TS 调用 `doEncrypt(...,0)`，因此服务端要求 **x||y||C2||C3**（此前误用 C1C3C2）。验证脚本为一次性运行，未入库。
   - 已补 `internal/v4/resources/v1.9.10-config.json`（`v1.9.10-be7fe1`，对应当前线上构建）；`/load` 的 pt 会以日志字段 `payload_protocol` 输出，线上出现 pt=2 时可直接确认。
   - 遗留：线上样本目前只观测到 pt=1，pt=2 只能靠上述交叉验证兜底；若后续抓到 pt=2 的真实 challenge，再按 `curl /pass_v4?captcha_id=...` 做一次真机回归。
5. **（已完成）Worker/IPC**：新增 `cmd/captcha-gateway` + `captcha-tools -worker`；V3/V4 各一个 worker 进程，Unix Socket，空闲 `WORKER_IDLE_TIMEOUT`（默认 1m）后进程退出，由 OS 回收模型内存。模型组不再进程内闲置卸载。
6. **离线镜像**：Dockerfile 已存在，需把 V3+V4 模型以**普通文件**（非 go:embed、非运行时下载）烤进镜像；resources 数据文件已 go:embed。Dockerfile 环境变量约定：`MODE=all`、`WORKER_IDLE_TIMEOUT=1m`、`WORKER_START_TIMEOUT=2m`、`WORKER_REQUEST_TIMEOUT=3m`、`use_pdl/use_dfine/use_multi/use_grid`、`LOG_LEVEL=INFO`、`DINO_RANDOM_OFFSET=1`、`ONNXRUNTIME_SHARED_LIBRARY_PATH=/usr/lib/libonnxruntime.so.1.31.0`。旧的 `MODEL_PRELOAD=false`、`MODEL_IDLE_TIMEOUT=10m` 不再作为推荐配置。
7. **测试与验收**：V3 金标基线回归（需真实 ONNX + libjpeg-turbo 环境）；V4 各求解器单测（必须 onnx-free，模型相关测试用假数据/跳过）；真机米哈游滑块+图标验证；50 次启停无僵尸进程；RSS/内存日志。

## 三、关键约定与技术要点（移植时逐条对照）

- **唯一事实来源**：Lotus 的 TS 源码，逐函数对照移植；不要重新发明算法。
- **模型位置**：`E:\dsh work\model-packs` 存放全部模型，**严禁重新下载**。V4 模型目录：v4-direction/icon-direction.onnx、v4-symbols/{symbol-detect,symbol-embedding}.onnx、v4-word/{word-detect,text-recognize}.onnx、v4-phrase-core/{phrase-detect,phrase-recognize,phrase-order}.onnx + phrase-dictionary.json + phrase-tokens.json、v4-phrase-lexicon/phrase-lexicon.txt、grid/{grid-vision,grid-head}.onnx。
- **缺失模型**：embedded-detect.onnx、dino（locateSymbols 用的）、hgnet 分类器位置未确认 —— symbolRegions 已改用纯 symbol-detect 路径规避。
- **detect() 统一路径**（detector.ts）：letterbox 到模型输入（默认 320，114/255 填充，无 BGR 交换）；输出 `transposed = dims[1] < dims[2]`；`at(n,c)=data[transposed?c*count+n:n*channels+c]`；score=max(at(n,4), at(n,c≥5))，label=c-4；box_format xyxy 或 cxcywh；clip→suppress(iou,limit)→trunc→过滤 w/h>0。Go 端：`vision.Letterbox` + `vision.DecodeDetect`。
- **icon-direction**：输入 [1,3,640,640]，names {0:u,1:d,2:l,3:r,4:lu,5:ld,6:ru,7:rd}，输出 [1,12,8400]（transposed），置信度 0.1 / iou 0.45 / limit 4。8 个提示词 md5 硬编码映射已在 icon_solvers.go。
- **symbol-detect**：[1,3,416,416] YOLOX，输出 [1,3549,6]（shape[1]==3549，非转置），无类别，置信度 0.5；nms 0.35；roundEven 裁剪；过滤 w/h≥12 且面积<0.6；取前 12。**注意在模糊图（BlurRGB 0.8）上做检测**。
- **symbol-embedding**：[1,3,96,96]，输出 [N,4608]；rotations 1/4/12（默认 1）；prompts 1..6；旋转用 `vision.RotateRGBWhite`（白底填充，sharp 兼容）；cosine 打分（`vision.CosineSimilarity32`）→ SymbolAssignment（DP 位掩码，行≤列，非有限值返回 nil）→ 任一 <0.3 报错 `no symbol embedding assignment`。
- **word-detect / phrase-detect**：LibreYOLO9t，输入 320，box_format xyxy，输出 [batch, 4+nc, anchors]（转置），names {0:'target'}；word 置信度 0.4/iou 0.45/limit 5；phrase 0.7 且必须 4 个框。
- **text-recognize / phrase-recognize**：输入 `x` [Dynamic,3,48,Dynamic]，输出 `fetch_name_0`；recTensor：高 48，宽 = floor(48*max(minRatio, 各图宽高比))，BGR 交换 `(2-c)`，归一化 `(v/255-0.5)/0.5`，右侧零填充；posterior 按 8 张一批，`frames=min(steps, max(1, round(min(width, ceil(48*w/h))/8)))`，取 c∈[1,classes) 之和最大帧。
- **phrase 识别**：CTC 贪心（去重复、跳 blank），字符表 = ocr-characters.json 按 \n 分割，前插 "blank" 尾插 " "，取第一个汉字（[\u4e00-\u9fff]）。
- **phrase 排序**（phraseOrder）：先查 phrase-dictionary.json（table: 排序字→短语），全唯一且命中→直接返回；有 lexicon（phrase-lexicon.txt，仅 4 个汉字词，key=排序后字符）且唯一候选→返回；否则 phrase-tokens.json vocab + phrase-order.onnx（input_ids/attention_mask→logits），遍历 4! 排列，`score += data[(char+1)*classes+position]`，排除 tried，优先 pending 提示，取最高分。
- **滑块**：vision/slide.go 已实现 Gray、双边滤波、形态学梯度、模板匹配（CCOEFF_NORMED），参考 Lotus slide.ts。
- **数据加载**：模型目录（ModelDir）运行时读取；静态数据 go:embed。**模型文件禁止 go:embed**（体积巨大、镜像层需独立）。
- **输出约定**：所有求解器返回归一化点 `(x+width/2)/imgW, (y+height/2)/imgH`。
- **错误文案**保持与 Lotus 一致（如 "insufficient symbol detections"、"direction not detected"、"no symbol embedding assignment"、"phrase must have four detections"、"phrase recognition failed"）。

## 四、验收标准（用户原话要点）

- V3 测试与金标全部通过（不得破坏现有基线）。
- V4 每个求解器有独立测试。
- 真机米哈游滑块+图标验证通过。
- 50 次启动/停止循环后无僵尸进程（worker 空闲 1 分钟退出）。
- RSS/内存日志正常。
- Docker 镜像不触发任何模型下载（模型烤进镜像）。
- `save_result=false` 时不得写临时图片。

## 五、环境与工作方法

- 本机是 Windows（E:\dsh work 工作区，Go 工具链缺失），你若有 Go 环境请直接 `go build ./...` 验证；否则按"逐行审阅 + 括号平衡检查 + 无用导入扫描"方式静态自检。
- 单元测试必须 onnx-free（模型相关走假数据或 t.Skip）。
- 保持 gofmt 风格；结构体字段对齐。
- 优先级按"剩余工作"顺序推进，每完成一项更新本文档。
