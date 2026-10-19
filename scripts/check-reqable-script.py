#!/usr/bin/env python3
"""检查 reqable 抓包脚本的入口与提交行为.

Reqable 的运行器对每个请求都会无条件调用 addons.onRequest, 对每个响应无条件调用
addons.onResponse, 缺少任何一个都会在抓包时报 AttributeError 并让脚本以退出码 1 结束.
本脚本用替身 reqable 模块加载真正的脚本文件, 按运行器的方式调用两个入口, 并用本地
HTTP 服务断言提交内容, 因此不需要安装 reqable, 也不需要下载服务真的在跑.

只依赖 Python 标准库.
"""

from __future__ import annotations

import importlib.util
import json
import sys
import threading
import types
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parent.parent
SCRIPT_PATH = PROJECT_ROOT / "assets" / "reqable" / "query-video-info.py"

TARGET_URL = "https://app.xiaoe-tech.com/query_video_info?sign=test"

VALID_PAYLOAD = {
    "code": 0,
    "msg": "",
    "data": {
        "0": {
            "file_name": "蓝桥杯嵌入式第三讲——Uart、DMA、ADC.mp4",
            "title": "Uart、DMA、ADC",
        },
        "video_info": {"src": "https://encrypt-k-vod.xet.tech/drm/v.f421220.m3u8?sign=test"},
    },
}

_received: list[dict] = []


class _CaptureHandler(BaseHTTPRequestHandler):
    """收下脚本提交的 JSON, 并返回与下载服务一致的响应."""

    def do_POST(self) -> None:  # noqa: N802 (http.server 要求的命名)
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length)
        _received.append(json.loads(raw.decode("utf-8")))
        body = b'{"code":0,"msg":"Added to queue"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt: str, *args) -> None:
        """静默默认的访问日志."""


class FakeContext:
    """替身 context, 只实现脚本用到的 url 与 env."""

    def __init__(self, url: str, env: dict | None = None):
        self.url = url
        self.env = env or {}


class FakeResponse:
    """替身 response, body 与 reqable 的 HttpBody 一样支持 str() 取文本."""

    def __init__(self, body, request=None):
        self.body = body
        self.request = request


class FakeRequest:
    """替身 request, onRequest 应当原样放行, 同时给诊断信息提供 method/path/body."""

    def __init__(self, method: str = "POST", path: str = "/query_video_info", body: str = ""):
        self.method = method
        self.path = path
        self.body = body


def install_reqable_stub() -> None:
    """装载空的 reqable 替身, 让脚本的 from reqable import * 可用."""
    sys.modules["reqable"] = types.ModuleType("reqable")


def load_addon() -> types.ModuleType:
    """把抓包脚本当成 runnable 的 addons 模块加载, 与 Reqable 的做法一致."""
    spec = importlib.util.spec_from_file_location("addons", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"无法加载脚本: {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main() -> int:
    install_reqable_stub()
    addon = load_addon()

    server = ThreadingHTTPServer(("127.0.0.1", 0), _CaptureHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    server_url = f"http://127.0.0.1:{server.server_address[1]}/upload-video-info"

    failures: list[str] = []

    def check(name: str, condition: bool, detail: str = "") -> None:
        if condition:
            print(f"通过: {name}")
        else:
            failures.append(name)
            print(f"失败: {name} {detail}".rstrip())

    # 运行器会无条件调用两个入口, 缺一个就会在抓包时报 AttributeError.
    check("定义了 onRequest", callable(getattr(addon, "onRequest", None)))
    check("定义了 onResponse", callable(getattr(addon, "onResponse", None)))
    if failures:
        server.shutdown()
        return 1

    request = FakeRequest()
    check("onRequest 原样放行请求", addon.onRequest(FakeContext(TARGET_URL), request) is request)

    def call_response(url: str, payload, server_override: str | None = server_url):
        env = {"XESTUDY_SERVER": server_override} if server_override else {}
        response = FakeResponse(payload if isinstance(payload, str) else json.dumps(payload, ensure_ascii=False))
        return addon.onResponse(FakeContext(url, env), response), response

    before = len(_received)
    response, same = call_response(TARGET_URL, VALID_PAYLOAD)
    check("onResponse 不改动原始响应", response is same)
    check("命中接口时提交一次任务", len(_received) - before == 1)
    if len(_received) - before == 1:
        check("提交内容与响应体一致", _received[-1] == VALID_PAYLOAD, json.dumps(_received[-1], ensure_ascii=False)[:120])

    ignored = [
        ("路径不匹配时不提交", FakeContext("https://app.xiaoe-tech.com/other_api", {"XESTUDY_SERVER": server_url}), VALID_PAYLOAD),
        ("接口返回非 0 时不提交", FakeContext(TARGET_URL, {"XESTUDY_SERVER": server_url}), {"code": 6, "msg": "未找到", "data": ""}),
        (
            "缺少 m3u8 地址时不提交",
            FakeContext(TARGET_URL, {"XESTUDY_SERVER": server_url}),
            {"code": 0, "data": {"0": {"file_name": "a.mp4"}, "video_info": {}}},
        ),
    ]
    for name, context, payload in ignored:
        before = len(_received)
        addon.onResponse(context, FakeResponse(json.dumps(payload, ensure_ascii=False)))
        check(name, len(_received) == before)

    before = len(_received)
    response = FakeResponse("<html>不是 JSON</html>")
    check("响应体不是 JSON 时原样返回", addon.onResponse(FakeContext(TARGET_URL, {"XESTUDY_SERVER": server_url}), response) is response)
    check("响应体不是 JSON 时不提交", len(_received) == before)

    # code 非 0 时要能指出请求侧的情况, 小鹅通页面打开时的探测请求就属于这一类.
    probe_body = json.dumps({"buz_data": {"video_id": "", "content_app_id": ""}, "app_id": "app3qiy6sow1137"}, ensure_ascii=False)
    probe = FakeResponse(json.dumps({"code": 6, "msg": "未找到", "data": ""}, ensure_ascii=False), FakeRequest(body=probe_body))
    lines = addon._describe_request(probe)
    check("code 非 0 时给出请求侧诊断", any("buz_data" in line for line in lines), str(lines))
    check("video_id 为空时点明是探测请求", any("探测请求" in line for line in lines), str(lines))
    check("取不到请求时诊断为空", addon._describe_request(FakeResponse("{}")) == [])

    before = len(_received)
    result = addon.onResponse(FakeContext(TARGET_URL, {"XESTUDY_SERVER": server_url}), probe)
    check("code 非 0 且带请求时原样放行", result is probe)
    check("code 非 0 且带请求时不提交", len(_received) == before)

    before = len(_received)
    response, same = call_response(TARGET_URL, VALID_PAYLOAD, server_override="http://127.0.0.1:1/upload-video-info")
    check("下载服务不可达时仍返回原始响应", response is same)
    check("下载服务不可达时不视为已提交", len(_received) == before)

    # 地址来自 Reqable 环境变量时也应当生效, 这里换一个端口再确认一次.
    server2 = ThreadingHTTPServer(("127.0.0.1", 0), _CaptureHandler)
    threading.Thread(target=server2.serve_forever, daemon=True).start()
    before = len(_received)
    call_response(TARGET_URL, VALID_PAYLOAD, server_override=f"http://127.0.0.1:{server2.server_address[1]}/upload-video-info")
    ok = len(_received) - before == 1
    server2.shutdown()
    check("XESTUDY_SERVER 覆盖生效", ok)

    server.shutdown()

    if failures:
        print(f"检查失败: {len(failures)} 项 ({', '.join(failures)})", file=sys.stderr)
        return 1
    print("reqable 脚本检查全部通过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
