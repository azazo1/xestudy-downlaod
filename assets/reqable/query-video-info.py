# 小鹅通视频下载 - Reqable 脚本 (Addon)
#
# 作用:
#   抓到小鹅通的响应后, 判断里面有没有能直接下载的 m3u8 播放列表地址, 有就交给本机的
#   xestudy-downlaod 服务去下载, 解密并拼接.
#
# 为什么按内容判断而不是按接口地址判断:
#   小鹅通客户端会换接口, 连接口名里都带着拼写错误 (video.mutli_line), 按固定路径匹配迟早失效.
#   这里改成: URL 落在采集范围内就解析响应, 谁给出 m3u8 地址就下载谁, 业务接口改了也照样能用.
#
# 使用步骤:
#   1. 启动下载服务:
#        xestudy-downlaod --savepath ./videos
#   2. Reqable 菜单 Traffic -> Script -> Enable 打开脚本功能;
#   3. Traffic -> Script -> Add Rule, 新建规则:
#        规则名:   小鹅通视频下载
#        匹配 URL: *xiaoe*
#   4. 把本文件内容整体粘贴到规则脚本编辑区, 保存;
#   5. 在小鹅通里点开视频播放, 服务端日志出现 "收到下载请求" 即为成功.
#
# 说明:
#   - onRequest 与 onResponse 两个入口都必须定义, 运行器会分别无条件调用它们;
#   - 脚本只依赖 Python 标准库, 无需 pip 安装额外的包;
#   - 采集范围可用 Reqable 环境变量 XESTUDY_MATCHES 覆盖 (逗号分隔的 URL 片段);
#   - 服务地址可用 XESTUDY_SERVER 覆盖 (可以是 http://host:port 或完整的提交地址);
#   - 响应里的视频标题会被记住最多 5 分钟, 供只带地址的响应复用, 位置可用 XESTUDY_STATE_FILE 覆盖.

from reqable import *

import json
import os
import re
import tempfile
import time
import traceback
import urllib.request

# 服务地址, 需要改端口时优先用环境变量 XESTUDY_SERVER.
SERVER_BASE = 'http://127.0.0.1:8010'

# 提交路径: 老结构走第一条, 只抽到地址时走第二条.
INFO_PATH = '/upload-video-info'
URL_PATH = '/upload-video-url'

# URL 里含其中任一片段就会进入分析; 客户端换域名时改这里或用 XESTUDY_MATCHES.
MATCHES = ['xiaoe']

# m3u8 地址匹配, 兼容正文里的普通写法与 JSON 转义写法.
M3U8_PATTERN = re.compile(r'https?://[^\s"\'\\<>]+\.m3u8[^\s"\'\\<>]*')

# 试看, 预览, 音频轨这类地址不作为下载目标.
EXCLUDE_HINTS = ('try', 'preview', 'sample', 'audio', 'trailer')

# 名称字段的优先级, 越靠前越可信; 不含裸 name, 免得把清晰度标签当成标题.
NAME_PRIORITY = ('file_name', 'filename', 'video_name', 'resource_name', 'title', 'video_title', 'course_title')

# 用于给地址字段打分的字段名.
URL_KEYS = ('src', 'url', 'play_url', 'video_url', 'line_url', 'm3u8', 'hls')

# 记住最近一次看到的标题, 供结构不同的响应之间共享.
STATE_FILE = os.path.join(tempfile.gettempdir(), 'xestudy-last-name.json')
STATE_TTL = 300.0

# 提交超时时间, 单位秒.
TIMEOUT = 10.0


def _env(context, name, default):
    try:
        value = context.env.get(name)
    except Exception:
        value = None
    return value or default


def _server_base(context):
    """兼容把 XESTUDY_SERVER 写成完整提交地址的情况."""
    base = _env(context, 'XESTUDY_SERVER', SERVER_BASE).rstrip('/')
    for suffix in (INFO_PATH, URL_PATH):
        if base.endswith(suffix):
            base = base[: -len(suffix)]
    return base


def _should_collect(context):
    """判断该请求是否在采集范围内."""
    raw = _env(context, 'XESTUDY_MATCHES', '')
    patterns = [item.strip() for item in raw.split(',') if item.strip()] or MATCHES
    url = context.url or ''
    return any(pattern in url for pattern in patterns)


def _unescape(text):
    """还原 JSON 里被转义的斜杠, 否则 m3u8 地址会认不出来."""
    if '\\' not in text:
        return text
    return text.replace('\\/', '/').replace('\\u002F', '/').replace('\\u002f', '/')


def _walk(node, path=()):
    """深度遍历 JSON, 产出 (字段路径, 字符串值)."""
    if isinstance(node, dict):
        for key, value in node.items():
            yield from _walk(value, path + (str(key),))
    elif isinstance(node, list):
        for index, value in enumerate(node):
            yield from _walk(value, path + (str(index),))
    elif isinstance(node, str):
        yield path, node


def _first_m3u8(text):
    """从一段文本里取出第一个 m3u8 地址."""
    match = M3U8_PATTERN.search(_unescape(text))
    return match.group(0) if match else ''


def _score_url(path, url):
    """给候选地址打分, 分数低于 0 表示不该下载."""
    lowered = url.lower()
    if any(hint in lowered for hint in EXCLUDE_HINTS):
        return -1

    score = 0
    key = path[-1].lower() if path else ''
    if key in URL_KEYS:
        score += 3
    if 'main' in lowered:
        score += 2
    if 'drm' in lowered:
        score += 1
    return score


def _pick_m3u8(text, payload):
    """挑出最像正片的 m3u8 地址, 优先取有明确字段名的写法."""
    scored = []
    if payload is not None:
        for path, value in _walk(payload):
            if 'm3u8' not in value:
                continue
            url = _first_m3u8(value)
            if url:
                scored.append((_score_url(path, url), url))

    if not scored:
        # 不是 JSON 或者地址散落在文本里时退化为正则扫描.
        for match in M3U8_PATTERN.finditer(_unescape(text)):
            url = match.group(0)
            scored.append((_score_url((), url), url))

    candidates = [item for item in scored if item[0] >= 0]
    if not candidates:
        return ''
    candidates.sort(key=lambda item: item[0], reverse=True)
    return candidates[0][1]


def _pick_name(payload):
    """按优先级抽出最可信的名称字段."""
    if payload is None:
        return ''

    found = {}
    for path, value in _walk(payload):
        key = path[-1].lower() if path else ''
        if key not in NAME_PRIORITY:
            continue
        text = value.strip()
        if not text or len(text) > 120:
            continue
        found.setdefault(key, text)

    for key in NAME_PRIORITY:
        if key in found:
            return found[key]
    return ''


def _legacy_src(payload):
    """老结构: data."0".file_name 与 data.video_info.src, 走它可沿用原有命名规则."""
    try:
        data = payload['data']
        file_name = data['0']['file_name']
        src = data['video_info']['src']
    except Exception:
        return ''
    if not file_name or not src:
        return ''
    return src if 'm3u8' in _unescape(src) else ''


def _state_path():
    return os.environ.get('XESTUDY_STATE_FILE') or STATE_FILE


def _remember_name(name):
    """记住标题, 写入失败不影响抓包."""
    try:
        with open(_state_path(), 'w', encoding='utf-8') as handle:
            json.dump({'name': name, 'at': time.time()}, handle, ensure_ascii=False)
    except Exception:
        pass


def _remembered_name():
    try:
        with open(_state_path(), encoding='utf-8') as handle:
            data = json.load(handle)
        if time.time() - float(data.get('at', 0)) > STATE_TTL:
            return ''
        return str(data.get('name') or '')
    except Exception:
        return ''


def _submit(context, path, payload, reason):
    """提交下载任务, 任何失败都只打印不抛出, 不影响抓包本身."""
    address = _server_base(context) + path
    body = json.dumps(payload, ensure_ascii=False).encode('utf-8')
    request = urllib.request.Request(
        address,
        data=body,
        headers={'Content-Type': 'application/json; charset=utf-8'},
    )
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT) as response:
            status = response.status
            text = response.read().decode('utf-8', 'replace').strip()
        print('[xestudy] 已提交下载任务 (%s): %s %s' % (reason, status, text))
    except Exception as error:
        print('[xestudy] 提交下载任务失败 (%s): %s' % (reason, error))
        print('[xestudy] 请确认下载服务已启动, 且地址与 XESTUDY_SERVER 一致')


def onRequest(context, request):
    # 运行器对每个请求都会无条件调用本函数, 因此即使不做任何事也必须定义并原样返回.
    return request


def onResponse(context, response):
    if not _should_collect(context):
        return response

    try:
        text = str(response.body)
    except Exception:
        return response

    payload = None
    stripped = text.lstrip()
    if stripped.startswith('{') or stripped.startswith('['):
        try:
            payload = json.loads(text)
        except Exception:
            payload = None

    try:
        # 老接口的结构继续走老通道, 保持原有命名规则 (文件名主干 + 标题).
        if payload is not None and _legacy_src(payload):
            _submit(context, INFO_PATH, payload, '沿用 video_info 结构')
            return response

        url = _pick_m3u8(text, payload)
        if not url:
            # 没有地址时顺手记下标题, 供只带地址的响应复用.
            name = _pick_name(payload)
            if name:
                _remember_name(name)
            return response

        name = _pick_name(payload) or _remembered_name()
        _submit(context, URL_PATH, {'url': url, 'name': name}, '从响应里抽取 m3u8')
    except Exception as error:
        print('[xestudy] 处理响应出错: %s' % error)
        traceback.print_exc()

    # 不改动原始响应, 保证抓包内容与真实响应一致.
    return response
