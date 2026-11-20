# 验证记录

验证日期：2026-11-20。以下只记录实际执行结果。

## 环境与模型

- 本机：Windows amd64。
- Go：1.27.4；C 编译器：MinGW GCC 16.2.0；CGo 开启。
- ONNX Runtime：1.31.0。
- libjpeg-turbo：3.1.4.1，与 Pillow 12.3.0 基线报告版本一致。
- Python 基线：Python 3.12.14、NumPy 2.2.6、OpenCV 4.12.0.88。
- 五个 ONNX 文件复制前后 SHA-256 一致，未删除或更换模型。

模型 SHA-256：

| 文件 | SHA-256 |
| --- | --- |
| PP-HGNetV2-B4.onnx | 96a67fada4a7289143fc279764cb0d976bcc194bb3c2ddb32ae0795478d00f2e |
| d-fine-n.onnx | e6508cd52b016f826ce2437e007d1f35c34a3fe2502ca69da5d448a07410e832 |
| yolo11n.onnx | 6443ec74e0756f64d3aaf75d5b6baffb3a5c2ae5789104b91ff701de31f94149 |
| dinov3-small.onnx | f0729237db38a442aa377f45ba38a223d9e11ea27f5bc9b6d5a71588d9ae3c2c |
| atten.onnx | 9f2544b549e6c259c0f7df5e95dc88fe09ceb63867b5206f0166a49e0d3239b8 |

## 已完成并通过

### 代码质量与单元/API 测试

- gofmt：全部 Go 文件无格式差异。
- go vet ./...：通过。
- go test -count=1 ./...：全部包通过。
- go test -race -count=1 ./...：全部包通过，未报告 Go 数据竞争。
- 服务可执行文件 go build：通过。
- 加密与协议测试：AES/RSA、自定义编码、Python 有序 JSON、鼠标轨迹、JSON/JSONP、七次 Mock 请求、payload 解密、Cookie、响应大小、body 关闭和 context 均通过。
- 图像测试：六种 Pillow RGB 缩放黄金向量逐字节一致；libjpeg 编码码流与解码 RGB 同 Pillow 黄金向量逐字节一致；切图、预处理、NMS、坐标和排序通过。
- 模型 fake-resource 测试：并发加载去重、闲置卸载、重载、在途保护、取消、重复关闭通过。
- Mock API：正常/错误响应、缺参数、模型错误、Hutao、模型管理和日志级别通过；未访问真实外部验证码服务。

### 真实离线模型对照

每组使用原项目 img_saved 下 9 张现有 JPG，由 Python 原版导出基线，再由 Go 真实 ONNX 推理：

| 组 | 结果 | 对照内容 |
| --- | --- | --- |
| PDL | 9/9 通过 | 主路径坐标集合及旧余弦分支顺序完全一致 |
| D-FINE | 9/9 通过 | 点选顺序一致；最终管线中心误差为 0 |
| Multi | 9/9 通过 | 随机偏移关闭；YOLO top/bottom 检测框、匹配顺序、中心完全一致 |

证据文件：

- testdata/baseline-pdl.json、testdata/report-pdl.json
- testdata/baseline-dfine.json、testdata/report-dfine.json
- testdata/baseline-multi.json、testdata/report-multi.json

最初使用 Go 标准库 JPEG 时曾发现旧 PDL 排序和 Multi NMS 差异；改为精确版本 libjpeg 后重新执行，以上最终结果才全部通过。没有删除样本或放宽断言。

### 真实 Session 生命周期与服务冒烟

- CAPTCHA_INTEGRATION=1 的真实 ONNX 测试通过：构造时不加载；只加载 PDL；在途租约阻止卸载；150ms 闲置后自动卸载；下一次识别自动重载。
- 独立测试端口 19645 的服务冒烟通过：初始 /list_model 只有表头；/load_model?name=pdl 实际加载；再次列表出现 session；/unload_model 后回到表头；/list_all_model、/set_log_level、非法日志 400、/healthz 和二进制 -healthcheck 通过。
- 该次实际 PDL 加载日志约 238ms，卸载约 7.7ms。测试进程已停止。

### 反复推理/卸载/重载与内存采样

- 三组各 12 轮：全部调用和卸载成功，无识别数量异常。
- Multi 追加 50 轮：全部成功；首次调用约 580ms，后续含重载调用平均约 534ms，最大约 584ms。
- 12 轮 PDL 的 RSS 约 43.5→49.4MiB；D-FINE 约 51.5→42.5MiB，存在波动。
- 50 轮 Multi 的 Go HeapAlloc 约 2.31→2.36MiB。RSS 从约 40.1MiB 上升，20 轮后主要在约 102–111MiB 区间波动，并多次短暂回落到约 46–49MiB；最后一轮约 105.7MiB。
- 该现象符合原生 arena/分配器保留内存的可能性；50 轮不能证明长期无泄漏，也不能据此承诺卸载后 RSS 立即下降。未设置内存目标未达标的虚假结论。

证据：testdata/memory-report.json、testdata/memory-report-multi50.json。

## 未完成或受环境限制

1. **未完成 Docker 验证**：当前主机没有 docker CLI/daemon，未执行 docker build、docker compose config、镜像大小测量或容器内识别。因此没有实际镜像大小，也未验证最终 Linux 镜像的 libjpeg/ONNX 链接。
2. **未完成 linux/arm64 实机构建**：Dockerfile 提供 amd64/arm64 分支及固定 SHA-256，但本机未构建。
3. **未调用真实 Geetest**：七请求流程由 Mock 和录制结构验证，线上有效性需部署后检查。
4. **真实点选业务样本不足**：原包样本都是九宫格 JPG；D-FINE/Multi 已完成同输入数值对照，但不是实际点选准确率评测。
5. **未做小时级 soak、生产 CPU/RSS 对比**：目前只有上述短时重复测试；不能替代部署后的并发、稳定性、内存和 CPU 对照。
6. **未修改或替换原 Python 容器**：没有停止、占用或替换 test-nine。

完成 Docker 部署后，应继续进行：启动检查、API 对照、懒加载/闲时卸载、重载延迟、内存/CPU、并发和长期稳定性验证。
