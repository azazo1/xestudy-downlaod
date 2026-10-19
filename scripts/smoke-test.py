#!/usr/bin/env python3
"""端到端自检脚本.

流程:
  1. 编译并启动 xestudy-downlaod, 监听 127.0.0.1 的随机端口;
  2. 另起一个假的 m3u8 服务, 提供播放列表与若干分片;
  3. 按 reqable 脚本的格式 POST 一次 /upload-video-info;
  4. 等待成片生成, 校验内容与分片目录结构;
  5. 清理进程与临时目录.

只依赖 Python 标准库, 跨平台可用. 为了不引入第三方加密库, 假服务提供的分片未加密,
AES-128 解密路径由 go test ./internal/downloader 覆盖.
"""

from __future__ import annotations

import json
import os
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parent.parent
SEGMENTS = [b"A" * 564, b"B" * 376]
EXPECTED = b"".join(SEGMENTS)


def free_port() -> int:
    """申请一个空闲端口."""
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class FakeCdn(BaseHTTPRequestHandler):
    """假的小鹅通视频源, 提供 m3u8 与分片."""

    def do_GET(self) -> None:  # noqa: N802 (http.server 要求的命名)
        path = self.path.split("?", 1)[0]
        routes = {
            "/playlist.m3u8": self.playlist_body,
            "/seg0.ts": lambda: SEGMENTS[0],
            "/seg1.ts": lambda: SEGMENTS[1],
        }
        if path not in routes:
            self.send_error(404)
            return

        body = routes[path]()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def playlist_body(self) -> bytes:
        playlist = "\n".join(
            [
                "#EXTM3U",
                "#EXT-X-VERSION:3",
                "#EXT-X-TARGETDURATION:2",
                "#EXT-X-MEDIA-SEQUENCE:0",
                "#EXTINF:2.000000,",
                "seg0.ts",
                "#EXTINF:2.000000,",
                "seg1.ts",
                "#EXT-X-ENDLIST",
                "",
            ]
        )
        return playlist.encode("utf-8")

    def log_message(self, fmt: str, *args) -> None:
        """静默默认的访问日志."""


def wait_for_health(address: str, timeout: float = 15.0) -> None:
    """等待下载服务的健康检查接口可用."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"http://{address}/healthz", timeout=1) as response:
                if response.status == 200:
                    return
        except Exception:
            time.sleep(0.2)
    raise RuntimeError("下载服务启动超时")


def submit(address: str, payload: dict) -> dict:
    """按 reqable 脚本的方式提交视频信息."""
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(
        f"http://{address}/upload-video-info",
        data=body,
        headers={"Content-Type": "application/json; charset=utf-8"},
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        return json.loads(response.read().decode("utf-8"))


def wait_for_output(path: Path, timeout: float = 30.0) -> None:
    """等待成片生成并校验内容."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if path.exists():
            if path.read_bytes() == EXPECTED:
                return
            raise RuntimeError("成片内容与分片拼接结果不一致")
        time.sleep(0.2)
    raise RuntimeError(f"等待成片超时: {path}")


def build_binary(target: Path) -> Path:
    """编译待测二进制."""
    binary = target / ("xestudy-downlaod.exe" if os.name == "nt" else "xestudy-downlaod")
    subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/xestudy-downlaod"],
        cwd=PROJECT_ROOT,
        check=True,
    )
    return binary


def run() -> int:
    """执行一次完整的端到端检查."""
    cdn = ThreadingHTTPServer(("127.0.0.1", 0), FakeCdn)
    threading.Thread(target=cdn.serve_forever, daemon=True).start()
    cdn_address = f"127.0.0.1:{cdn.server_address[1]}"

    with tempfile.TemporaryDirectory(prefix="xestudy-smoke-") as workspace:
        workspace_path = Path(workspace)
        binary = build_binary(workspace_path)
        address = f"127.0.0.1:{free_port()}"
        output_dir = workspace_path / "output"

        service = subprocess.Popen(
            [
                str(binary),
                "--bind",
                address,
                "--savepath",
                str(output_dir),
                "--concurrent",
                "4",
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        try:
            wait_for_health(address)
            print(f"[smoke] 下载服务已就绪: {address}")

            response = submit(
                address,
                {
                    "code": 0,
                    "msg": "",
                    "data": {
                        "0": {"file_name": "蓝桥杯嵌入式第三讲.mp4", "title": "Uart、DMA、ADC"},
                        "video_info": {"src": f"http://{cdn_address}/playlist.m3u8?sign=smoke"},
                    },
                },
            )
            print(f"[smoke] 提交结果: {response}")
            if response.get("code") != 0:
                raise RuntimeError(f"提交下载任务失败: {response}")

            wait_for_output(output_dir / "videos" / "蓝桥杯嵌入式第三讲-Uart、DMA、ADC.mp4")
            print("[smoke] 成片内容校验通过")

            segments = sorted(path.name for path in (output_dir / "segments" / "蓝桥杯嵌入式第三讲-Uart、DMA、ADC").iterdir())
            if segments != ["00000.ts", "00001.ts"]:
                raise RuntimeError(f"分片目录内容异常: {segments}")
            print(f"[smoke] 分片目录校验通过: {segments}")

            meta_files = list((output_dir / "meta").glob("*.json"))
            if len(meta_files) != 1:
                raise RuntimeError(f"元数据文件数量异常: {meta_files}")
            print(f"[smoke] 元数据已落盘: {meta_files[0].name}")
        finally:
            service.terminate()
            try:
                output = service.communicate(timeout=10)[0]
            except subprocess.TimeoutExpired:
                service.kill()
                output = ""

    print("[smoke] 全部检查通过")
    if output:
        print("[smoke] 服务输出:")
        print(output.rstrip())
    return 0


if __name__ == "__main__":
    try:
        sys.exit(run())
    except Exception as error:  # noqa: BLE001 (脚本需要把失败原因完整打印出来)
        print(f"[smoke] 检查失败: {error}", file=sys.stderr)
        sys.exit(1)
