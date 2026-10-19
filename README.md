# 小鹅通视频下载工具 (Go)

配合 Reqable 抓包使用的小鹅通视频下载服务, 对应的 Rust 版本为 `xestudy-video-download`.

原理: Reqable 抓到 <https://app.xiaoe-tech.com/query_video_info> 的响应, 由抓包脚本把响应体转发给本服务;
服务从响应里取出 `data.video_info.src` 指向的 m3u8 地址与 `data."0"` 中的文件名/标题,
并发下载分片, 按需做 AES-128-CBC 解密, 最后拼接成完整视频.

## 快速开始

```shell
# 1. 构建
just build

# 2. 启动下载服务, 视频保存到 ./videos
./bin/xestudy-downlaod --savepath ./videos --debug

# 3. 按下一节配置 reqable, 然后在客户端里播放视频
```

服务默认监听 `0.0.0.0:8010`, 接口:

| 接口 | 方法 | 说明 |
| --- | --- | --- |
| `/upload-video-info` | POST | 接收抓包脚本回传的视频信息并加入下载队列 |
| `/upload-video-url` | POST | 直接提交 m3u8 地址与文件名, 用于接口结构变化后的适配 |
| `/healthz` | GET | 存活检查, 确认脚本要提交的地址是否可达 |
| `/sniff` | POST / DELETE | 接收抓包诊断记录 / 清空记录, 需要 `--sniff` |
| `/sniff/report` | GET | 查看诊断报告, 需要 `--sniff` |
| `/sniff/records` | GET | 导出完整记录, 支持 `match` 与 `limit` 参数, 需要 `--sniff` |

## 配置 reqable

1. 打开 reqable 的脚本功能: 菜单 `Traffic` -> `Script` -> `Enable`;
2. 新建规则: `Traffic` -> `Script` -> `Add Rule`, 匹配地址填 `*xiaoe*`;
3. 把 [query-video-info.py](assets/reqable/query-video-info.py) 的内容整体粘贴到规则脚本编辑区, 保存;
4. 在小鹅通客户端或小程序里播放视频, 服务端日志出现 `收到下载请求` 即为成功.

规则地址写得宽一点是有意的: 脚本按**响应内容**判断要不要下载, 而不是按接口地址. 小鹅通客户端
换过接口 (甚至接口名里带着拼写错误 `video.mutli_line`), 按固定路径匹配迟早失效, 现在的逻辑是
URL 落在采集范围内就解析响应, 谁给出 m3u8 地址就下载谁:

- 响应还是老的 `data."0" + data.video_info.src` 结构时, 整个响应体提交给 `/upload-video-info`,
  沿用 `{文件名主干}-{标题}.{后缀}` 的命名;
- 其他结构时, 从响应里抽出 m3u8 地址与标题, 提交给 `/upload-video-url`; 标题字段按
  `file_name`, `video_name`, `title` 等顺序取值, 试看与预览地址会被排除;
- 只带地址的响应会复用最近 5 分钟内记住的标题, 避免把文件命名成时间戳.

脚本只依赖 Python 标准库. 环境变量 (在 reqable 的全局环境变量里设置即可, 不必改脚本):

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `XESTUDY_SERVER` | `http://127.0.0.1:8010` | 下载服务地址, 也可以写成完整的提交地址 |
| `XESTUDY_MATCHES` | `xiaoe` | 逗号分隔的 URL 片段, 决定分析哪些响应 |
| `XESTUDY_STATE_FILE` | 系统临时目录 | 记住标题用的状态文件位置 |

注意 Reqable 的运行器对每个请求都会无条件调用 `onRequest`, 对每个响应无条件调用 `onResponse`,
所以脚本里两个函数都必须定义, 缺一个就会在调试控制台报 `module 'addons' has no attribute ...`
并让脚本以退出码 1 结束.

### 抓包诊断

万一以后又换接口, 用诊断模式把所有 xiaoe 流量收下来分析:

1. 带诊断参数启动服务:

   ```shell
   ./bin/xestudy-downlaod --sniff --savepath ./videos --debug
   ```

2. 在 Reqable 里再加一条脚本规则 (与下载规则并存), 匹配地址填 `*xiaoe*`,
   内容粘贴 [xiaoe-sniffer.py](assets/reqable/xiaoe-sniffer.py);
3. 在小鹅通里点开视频并播放几秒, 服务端日志出现 `发现候选接口` 说明抓到了;
4. 打开 <http://127.0.0.1:8010/sniff/report> 看报告: `hint` 直接给出规则匹配地址该怎么写,
   `endpoints` 列出各接口是否出现 m3u8, 请求是否带非空 `video_id`;
5. 报告里只有正文摘要, 需要看某个接口的完整请求与响应时按 URL 过滤导出:

   ```shell
   curl -s 'http://127.0.0.1:8010/sniff/records?match=mutli_line' | jq '.records[0].response_body'
   ```

6. 下一轮诊断前清空记录:

   ```shell
   curl -X DELETE http://127.0.0.1:8010/sniff
   ```

诊断记录只存在内存里, 单条正文保存上限 64 KiB, 报告里每条记录只保留 512 字节摘要.
平时用不着诊断, 把那条规则禁用即可, 免得每个请求都多跑一次脚本.

不接 reqable 时, 也可以手工提交一次任务:

```shell
curl -X POST http://127.0.0.1:8010/upload-video-info \
  -H 'Content-Type: application/json' \
  --data @assets/samples/upload-video-info.sample.json
```

## 命令行参数

| 参数 | 简写 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--bind` | `-b` | `0.0.0.0:8010` | 服务监听地址 |
| `--savepath` | `-s` | 用户下载目录 | 视频保存目录, 支持 `~` 开头 |
| `--concurrent` | `-c` | `12` | 单个视频的分片下载并发数 |
| `--queue-size` | | `100` | 等待下载的任务数量上限 |
| `--ffmpeg` | | 关闭 | 使用 ffmpeg 拼接分片, 默认直接拼接字节流 |
| `--debug` | | 关闭 | 输出 debug 级别日志 |

## 输出目录

```text
<savepath>/
- meta/       # 抓包回传的原始信息, 便于回溯下载来源
- segments/   # 分片, 每个视频一个子目录, 按 00000.ts 顺序命名
- videos/     # 拼接完成的成片
```

分片保存在 `segments` 下, 因此中断后重新触发同一个视频即可续传, 已下载的分片不会被重复请求.
只有当全部分片都下载成功时才会拼接成片, 避免产出内容不完整的视频.

## 开发

| 命令 | 说明 |
| --- | --- |
| `just deps` | 拉取依赖 |
| `just build` | 构建二进制到 `bin/` |
| `just serve` | 前台启动服务, 保存到 `./videos` |
| `just test` | 运行单元测试, 不访问外网 |
| `just smoke` | 端到端自检: 起假视频源, 提交任务并校验成片 |
| `just sniff` | 带抓包诊断启动服务, 用于定位视频信息来自哪个接口 |
| `just check-reqable` | 检查 reqable 脚本的入口与提交行为, 不需要安装 reqable |
| `just vet` | 静态检查 |
| `just fmt` | 代码格式化 |

代码结构:

```text
cmd/xestudy-downlaod/         # 程序入口
internal/cli/                 # 命令行参数, 服务启动与优雅退出
internal/config/              # 运行参数与默认值
internal/server/              # HTTP 接口, 元数据落盘, 任务入队
internal/downloader/          # 分片下载, 解密, 拼接
internal/hls/                 # m3u8 解析
internal/naming/              # 视频文件名组合与净化
internal/model/               # 抓包回传的 JSON 结构
internal/logging/             # 日志初始化
assets/reqable/               # reqable 抓包脚本
assets/samples/               # 提交接口的示例请求体
scripts/                      # 端到端自检与发布脚本
docs/changelog/               # 各版本的发布说明
```

## 与 Rust 版的差异

- 日志改用标准库 `log/slog`, 关键阶段都有结构化日志;
- 启动时会先监听端口再进入服务循环, 端口占用能立刻报错而不是进到服务里才发现;
- 收到 Ctrl+C 时先停止接收新任务并等待当前任务结束, 再按一次则立即取消下载;
- 未显式给出 IV 时按 HLS 规范用分片序号推导, 而不是直接用全零 IV;
- 只要有分片下载失败就不生成成片, 避免得到内容残缺的视频;
- 文件名净化会顺带处理空文件名, 超长文件名与结尾的非法字符.

## 说明

下载到的视频仅供个人学习使用, 请遵守课程平台的服务条款.
