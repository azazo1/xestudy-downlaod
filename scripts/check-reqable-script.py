#!/usr/bin/env python3
"""检查 reqable 抓包脚本的入口与提交行为.

Reqable 的运行器对每个请求都会无条件调用 addons.onRequest, 对每个响应无条件调用
addons.onResponse, 缺少任何一个都会在抓包时报 AttributeError 并让脚本以退出码 1 结束.
本脚本用替身 reqable 模块加载真正的脚本文件, 按运行器的方式调用两个入口, 并用本地
HTTP 服务断言提交的路径与内容, 因此不需要安装 reqable, 也不需要下载服务真的在跑.

覆盖两个脚本: 下载用的 query-video-info.py 与诊断用的 xiaoe-sniffer.py.

只依赖 Python 标准库.
"""

from __future__ import annotations

import importlib.util
import json
import os
import sys
import tempfile
import threading
import types
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parent.parent
SCRIPT_PATH = PROJECT_ROOT / "assets" / "reqable" / "query-video-info.py"
SNIFFER_PATH = PROJECT_ROOT / "assets" / "reqable" / "xiaoe-sniffer.py"

TARGET_URL = "https://app.xiaoe-tech.com/query_video_info?sign=test"
NEW_API_URL = "https://xestudy.xiaoeknow.com/xe.course.business.video.mutli_line/1.0.0"
OTHER_URL = "https://example.com/other_api"

M3U8_ESCAPED = "https:\\/\\/htv-vod.h5.xed.plus\\/a83\\/app3qiy6sow1137\\/video\\/abc\\/drm\\/main.m3u8?sign=abc"
M3U8_PLAIN = "https://htv-vod.h5.xed.plus/a83/app3qiy6sow1137/video/abc/drm/main.m3u8?sign=abc"

LEGACY_PAYLOAD = {
    "code": 0,
    "msg": "",
    "data": {
        "0": {
            "file_name": "蓝桥杯嵌入式第三讲——Uart、DMA、ADC.mp4",
            "title": "Uart、DMA、ADC",
        },
        "video_info": {"src": M3U8_PLAIN},
    },
}

# 新结构: 标题与地址在完全不同的字段里, 没有老接口那套 data."0" 与 video_info.
NEW_PAYLOAD = {
    "code": 0,
    "data": {
        "title": "第三讲 Uart、DMA、ADC",
        "lines": [
            {"line_name": "高清", "play_url": M3U8_ESCAPED},
            {"line_name": "标清", "play_url": "https://htv-vod.h5.xed.plus/a83/other/main.m3u8?sign=def"},
        ],
    },
}

_received: list[dict] = []
_paths: list[str] = []


class _CaptureHandler(BaseHTTPRequestHandler):
    """收下脚本提交的 JSON, 并返回与下载服务一致的响应."""

    def do_POST(self) -> None:  # noqa: N802 (http.server 要求的命名)
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length)
        _received.append(json.loads(raw.decode("utf-8")))
        _paths.append(self.path.split("?", 1)[0])
        body = b'{"code":0,"msg":"Added to queue"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt: str, *args) -> None:
        """静默默认的访问日志."""


class FakeContext:
    """替身 context, 只实现脚本用到的 url, env 与 uid."""

    def __init__(self, url: str, env: dict | None = None, uid: str = "1-2-3"):
        self.url = url
        self.env = env or {}
        self.uid = uid


class FakeResponse:
    """替身 response, body 与 reqable 的 HttpBody 一样支持 str() 取文本."""

    def __init__(self, body, request=None, code: int = 200):
        self.body = body
        self.request = request
        self.code = code


class FakeRequest:
    """替身 request, onRequest 应当原样放行, 同时给诊断信息提供 method/path/body."""

    def __init__(self, method: str = "POST", path: str = "/query_video_info", body: str = ""):
        self.method = method
        self.path = path
        self.body = body


def install_reqable_stub() -> None:
    """装载空的 reqable 替身, 让脚本的 from reqable import * 可用."""
    sys.modules["reqable"] = types.ModuleType("reqable")


def load_addon(path: Path, name: str) -> types.ModuleType:
    """把抓包脚本当成 runnable 的 addons 模块加载, 与 Reqable 的做法一致."""
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"无法加载脚本: {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main() -> int:
    install_reqable_stub()
    state_file = Path(tempfile.mkdtemp(prefix="xestudy-check-")) / "last-name.json"
    os.environ["XESTUDY_STATE_FILE"] = str(state_file)

    addon = load_addon(SCRIPT_PATH, "addons")
    sniffer = load_addon(SNIFFER_PATH, "addons_sniffer")

    server = ThreadingHTTPServer(("127.0.0.1", 0), _CaptureHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base_url = f"http://127.0.0.1:{server.server_address[1]}"
    env = {"XESTUDY_SERVER": base_url}

    failures: list[str] = []

    def check(name: str, condition: bool, detail: str = "") -> None:
        if condition:
            print(f"通过: {name}")
        else:
            failures.append(name)
            print(f"失败: {name} {detail}".rstrip())

    def call(url: str, body, context_env: dict | None = env, uid: str = "1-2-3"):
        """按运行器的方式调用 onResponse, 返回 (返回值, 是否与传入对象一致)."""
        response = FakeResponse(body if isinstance(body, str) else json.dumps(body, ensure_ascii=False))
        result = addon.onResponse(FakeContext(url, context_env if context_env is not None else env, uid), response)
        return result, result is response

    def last_submission():
        if not _received:
            return "", {}
        return _paths[-1], _received[-1]

    # 运行器会无条件调用两个入口, 缺一个就会在抓包时报 AttributeError.
    check("下载脚本定义了 onRequest", callable(getattr(addon, "onRequest", None)))
    check("下载脚本定义了 onResponse", callable(getattr(addon, "onResponse", None)))
    check("诊断脚本定义了 onRequest", callable(getattr(sniffer, "onRequest", None)))
    check("诊断脚本定义了 onResponse", callable(getattr(sniffer, "onResponse", None)))
    if failures:
        server.shutdown()
        return 1

    request = FakeRequest()
    check("下载脚本原样放行请求", addon.onRequest(FakeContext(TARGET_URL, env), request) is request)

    # 老结构继续走老通道, 保持原有命名规则.
    before = len(_received)
    _, same = call(TARGET_URL, LEGACY_PAYLOAD)
    check("老结构不改动原始响应", same)
    check("老结构提交一次任务", len(_received) - before == 1)
    path, body = last_submission()
    if len(_received) - before == 1:
        check("老结构走 upload-video-info", path == "/upload-video-info", path)
        check("老结构提交内容与响应体一致", body == LEGACY_PAYLOAD)

    # 新结构: 只抽地址与标题, 走直传通道.
    before = len(_received)
    call(NEW_API_URL, NEW_PAYLOAD)
    check("新结构提交一次任务", len(_received) - before == 1)
    if len(_received) - before == 1:
        path, body = last_submission()
        check("新结构走 upload-video-url", path == "/upload-video-url", path)
        check("新结构抽到转义后的地址", body.get("url") == M3U8_PLAIN, json.dumps(body, ensure_ascii=False))
        check("新结构取到标题字段", body.get("name") == "第三讲 Uart、DMA、ADC", json.dumps(body, ensure_ascii=False))

    # 试看与预览地址不作为下载目标.
    before = len(_received)
    call(NEW_API_URL, {"code": 0, "data": {"lines": [{"play_url": "https://a.com/try/main.m3u8?sign=x"}]}})
    check("试看地址不提交", len(_received) == before)

    # 纯文本响应 (不是 JSON) 里的地址也能兜住.
    before = len(_received)
    call(NEW_API_URL, f"prefix {M3U8_PLAIN} suffix")
    check("非 JSON 正文里的地址也能抽到", len(_received) - before == 1)

    # 只有标题没有地址时先记住, 之后只带地址的响应复用这个标题.
    state_file.unlink(missing_ok=True)
    before = len(_received)
    call(NEW_API_URL, {"code": 0, "data": {"title": "第九讲 SPI 与 I2C"}})
    check("只有标题时不提交", len(_received) == before)
    check("标题被记住", state_file.exists())

    before = len(_received)
    call(NEW_API_URL, {"code": 0, "data": {"lines": [{"play_url": M3U8_PLAIN}]}})
    check("随后仅带地址的响应被提交", len(_received) - before == 1)
    if len(_received) - before == 1:
        check("复用了记住的标题", last_submission()[1].get("name") == "第九讲 SPI 与 I2C")

    # 无地址无标题的普通响应应当安静跳过.
    before = len(_received)
    _, same = call(TARGET_URL, {"code": 6, "msg": "未找到", "data": ""})
    check("普通响应原样返回", same)
    check("普通响应不提交", len(_received) == before)

    # 不在采集范围的 URL 直接放行.
    before = len(_received)
    call(OTHER_URL, NEW_PAYLOAD, context_env={"XESTUDY_SERVER": base_url, "XESTUDY_MATCHES": "xiaoe"})
    check("不在采集范围不提交", len(_received) == before)

    # 采集范围可用环境变量放宽.
    before = len(_received)
    call(OTHER_URL, NEW_PAYLOAD, context_env={"XESTUDY_SERVER": base_url, "XESTUDY_MATCHES": "example.com"})
    check("XESTUDY_MATCHES 覆盖生效", len(_received) - before == 1)

    # 服务不可达时不能抛异常, 也不能丢掉原始响应.
    before = len(_received)
    response = FakeResponse(json.dumps(LEGACY_PAYLOAD, ensure_ascii=False))
    result = addon.onResponse(FakeContext(TARGET_URL, {"XESTUDY_SERVER": "http://127.0.0.1:1"}), response)
    check("服务不可达时原样返回响应", result is response)
    check("服务不可达时不视为已提交", len(_received) == before)

    # 诊断脚本: 覆盖全部 xiaoe 请求, 把请求与响应都回传给服务端分析.
    sniff_env = {"XESTUDY_SNIFF_SERVER": f"{base_url}/sniff"}
    probe_request = FakeRequest(body=json.dumps({"buz_data": {"video_id": "v_1"}}, ensure_ascii=False))
    before = len(_received)
    passed = sniffer.onRequest(FakeContext(TARGET_URL, sniff_env), probe_request)
    check("诊断脚本原样放行请求", passed is probe_request)
    check("请求阶段回传一条记录", len(_received) - before == 1)
    if len(_received) - before == 1:
        entry = _received[-1]
        check(
            "请求记录带阶段与请求体",
            entry.get("phase") == "request" and "v_1" in entry.get("request_body", ""),
            json.dumps(entry, ensure_ascii=False)[:120],
        )

    before = len(_received)
    sniffer.onResponse(FakeContext(TARGET_URL, sniff_env), FakeResponse(json.dumps(LEGACY_PAYLOAD, ensure_ascii=False)))
    check("响应阶段回传一条记录", len(_received) - before == 1)

    before = len(_received)
    sniffer.onResponse(FakeContext(OTHER_URL, sniff_env), FakeResponse("{}"))
    check("诊断脚本跳过不在范围的 URL", len(_received) == before)

    before = len(_received)
    sniffer.onResponse(FakeContext(TARGET_URL, sniff_env), FakeResponse("x" * (300 * 1024)))
    check("超长正文会被截断", len(_received) - before == 1 and _received[-1].get("truncated") is True)

    server.shutdown()

    if failures:
        print(f"检查失败: {len(failures)} 项 ({', '.join(failures)})", file=sys.stderr)
        return 1
    print("reqable 脚本检查全部通过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
