# 小鹅通视频下载 - Reqable 脚本 (Addon)
#
# 作用:
#   抓到小鹅通视频信息接口的响应后, 把响应体整体转发给本机的 xestudy-downlaod 服务,
#   由服务端解析 m3u8 地址并下载, 解密, 拼接视频.
#
# 使用步骤:
#   1. 终端里启动下载服务:
#        xestudy-downlaod --savepath ./videos
#      不确定服务是否活着时, 浏览器访问 http://127.0.0.1:8010/healthz
#   2. Reqable 菜单 Traffic -> Script -> Enable 打开脚本功能;
#   3. Traffic -> Script -> Add Rule 新建规则:
#        规则名:   小鹅通视频下载
#        匹配 URL: *video.mu*line*
#      小鹅通把 multi 写成了 mutli, 这样写两种拼写都能命中; 老接口那条规则
#      (匹配 https://app.xiaoe-tech.com/query_video_info) 可以继续留着.
#   4. 把本文件内容整体粘贴到规则脚本编辑区, 保存后即可;
#   5. 在小鹅通客户端/小程序里播放视频, 服务端会自动收到下载任务.
#
# 说明:
#   - onRequest 与 onResponse 两个入口都必须定义, 运行器会分别无条件调用它们;
#   - 脚本只依赖 Python 标准库, 无需 pip 安装额外的包;
#   - onRequest 与 onResponse 运行在不同进程, 本脚本只使用 onResponse;
#   - 规则匹配地址尽量写窄: 每命中一次都要新起一个 Python 进程, 匹配太宽会让抓包变卡;
#   - 服务地址默认是本机 8010 端口, 需要改端口时优先用 Reqable 环境变量 XESTUDY_SERVER,
#     其次才改下面的 SERVER_URL.

from reqable import *

import json
import re
import traceback
import urllib.request

# 下载服务的提交接口, 端口需与 --bind 一致.
SERVER_URL = 'http://127.0.0.1:8010/upload-video-info'

# 只抽到地址时走这个接口, 由服务端负责命名.
URL_PATH = '/upload-video-url'

# 处理范围内的路径片段. 小鹅通把 multi 拼成了 mutli (video.mutli_line), 两种写法都收,
# 万一以后改回 multi_line 也不用动脚本; 规则匹配地址写成 *video.mu*line* 即可, 不必放宽到整个域名.
TARGET_PATH_HINTS = ('/query_video_info', 'mutli_line', 'multi_line')

# 从响应里找 m3u8 地址; 转义写法先还原再匹配.
M3U8_PATTERN = re.compile(r'https?://[^\s"\'\\<>]+\.m3u8[^\s"\'\\<>]*')
EXCLUDE_HINTS = ('try', 'preview')

# 名称字段的优先级, 越靠前越可信; 不含裸 name, 免得把清晰度标签当成标题.
NAME_KEYS = ('file_name', 'filename', 'video_name', 'resource_name', 'title', 'video_title')

# 提交超时时间, 单位秒.
TIMEOUT = 10.0


def _server_url(context):
    """优先读取 Reqable 环境变量, 方便在不改脚本的情况下切换服务地址."""
    try:
        address = context.env.get('XESTUDY_SERVER')
    except Exception:
        address = None
    return address or SERVER_URL


def _server_base(context):
    """取服务根地址: XESTUDY_SERVER 可以写成根地址, 也可以写成完整的提交地址."""
    base = _server_url(context).rstrip('/')
    for suffix in ('/upload-video-info', URL_PATH):
        if base.endswith(suffix):
            base = base[: -len(suffix)]
    return base


def _find_m3u8(text):
    """取响应里第一个可下载的 m3u8 地址, 跳过试看与预览."""
    normalized = text.replace('\\/', '/').replace('\\u002F', '/').replace('\\u002f', '/')
    for match in M3U8_PATTERN.finditer(normalized):
        url = match.group(0)
        if any(hint in url.lower() for hint in EXCLUDE_HINTS):
            continue
        return url
    return ''


def _pick_name(payload):
    """按优先级从响应里找一个像标题的字段."""
    if not isinstance(payload, dict):
        return ''

    found = {}

    def walk(node):
        if isinstance(node, dict):
            for key, value in node.items():
                if isinstance(value, str) and str(key).lower() in NAME_KEYS:
                    text = value.strip()
                    if text and len(text) <= 120:
                        found.setdefault(str(key).lower(), text)
                walk(value)
        elif isinstance(node, list):
            for item in node:
                walk(item)

    walk(payload)
    for key in NAME_KEYS:
        if key in found:
            return found[key]
    return ''


def _submit_task(context, address, payload, reason):
    """提交下载任务; 失败只打印, 不影响抓包."""
    try:
        status, text = _submit(address, payload)
        print('[xestudy] 已提交下载任务 (%s): %s %s' % (reason, status, text.strip()))
    except Exception as error:
        print('[xestudy] 提交下载任务失败 (%s): %s' % (reason, error))
        print('[xestudy] 请确认下载服务已启动, 且地址与 SERVER_URL 一致')


def _submit(address, payload):
    """把响应体提交给下载服务, 返回 (状态码, 响应文本)."""
    body = json.dumps(payload, ensure_ascii=False).encode('utf-8')
    request = urllib.request.Request(
        address,
        data=body,
        headers={'Content-Type': 'application/json; charset=utf-8'},
    )
    with urllib.request.urlopen(request, timeout=TIMEOUT) as response:
        return response.status, response.read().decode('utf-8', 'replace')


def onRequest(context, request):
    # Reqable 的脚本运行器对每个请求都会无条件调用 onRequest, 所以即使这里什么都不做
    # 也必须定义, 否则会抛 "module 'addons' has no attribute 'onRequest'" 并以退出码 1 结束.
    # 下载所需的视频信息在响应里, 因此这里只原样放行.
    return request


def _describe_request(response):
    """从响应侧的请求里摘出诊断信息, 返回若干行文本.

    code 非 0 时用它说明请求侧的情况: 小鹅通的探测请求里 buz_data 是空的,
    这种情况服务端必然返回 "未找到", 与脚本无关.
    """
    request = getattr(response, 'request', None)
    if request is None:
        return []

    lines = []
    method = getattr(request, 'method', '?')
    path = getattr(request, 'path', '?')
    lines.append('[xestudy] 该响应来自 %s %s' % (method, path))

    try:
        body = str(request.body)
        if body.startswith('{'):
            data = json.loads(body)
            buz_data = data.get('buz_data')
            if isinstance(buz_data, dict):
                lines.append('[xestudy] 请求 buz_data=%s' % json.dumps(buz_data, ensure_ascii=False))
                if not buz_data.get('video_id'):
                    lines.append('[xestudy] video_id 为空, 这是页面打开时的探测请求, 点开并播放视频后的请求才带 video_id')
    except Exception:
        # 诊断信息取不到不影响主流程, 响应照常放行.
        pass

    return lines


def onResponse(context, response):
    # 规则本身已经按 URL 匹配, 这里再确认一次路径, 避免规则被改宽后误动作.
    path = context.url.split('?', 1)[0]
    if not any(hint in path for hint in TARGET_PATH_HINTS):
        print('[xestudy] 规则命中了 %s, 但路径不在处理范围内, 已跳过' % path)
        return response

    try:
        text = str(response.body)
    except Exception:
        print('[xestudy] 读不到响应体, 已忽略')
        return response

    try:
        payload = json.loads(text)
    except Exception:
        payload = None

    # 只有明确给出非 0 的 code 才算失败; 新接口不一定带这个字段.
    if isinstance(payload, dict) and payload.get('code') not in (None, 0):
        print('[xestudy] 接口返回 code=%s, 跳过: %s' % (payload.get('code'), payload.get('msg')))
        for line in _describe_request(response):
            print(line)
        print('[xestudy] 常见原因: 请求里 video_id 为空, 或 kotoken/token 登录态已过期; 重新登录并点开视频重试')
        return response

    # 老结构: 整个响应体交给服务端, 沿用 "文件名主干-标题" 的命名规则.
    try:
        legacy_content = payload['data']['0']
        legacy_src = payload['data']['video_info']['src']
    except Exception:
        legacy_content, legacy_src = None, ''

    if legacy_content and legacy_src:
        print('[xestudy] 捕获视频: %s' % (legacy_content.get('file_name') or legacy_content.get('title') or '未知'))
        print('[xestudy] m3u8 地址: %s' % legacy_src)
        _submit_task(context, _server_url(context), payload, '沿用 video_info 结构')
        return response

    # 新结构 (例如 xe.course.business.video.mutli_line): 响应里只要有 m3u8 地址就抽出来.
    url = _find_m3u8(text)
    if not url:
        print('[xestudy] %s 响应里没有 m3u8 地址, 跳过' % path)
        return response

    name = _pick_name(payload)
    print('[xestudy] 从 %s 抽到 m3u8 地址: %s' % (path, url))
    _submit_task(context, _server_base(context) + URL_PATH, {'url': url, 'name': name}, '从响应里抽取 m3u8')

    # 不改动原始响应, 保证抓包内容与真实响应一致.
    return response
