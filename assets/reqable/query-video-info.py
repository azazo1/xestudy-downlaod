# 小鹅通视频下载 - Reqable 脚本 (Addon)
#
# 作用:
#   抓到 https://app.xiaoe-tech.com/query_video_info 的响应后, 把响应体整体转发给
#   本机的 xestudy-downlaod 服务, 由服务端解析 m3u8 地址并下载, 解密, 拼接视频.
#
# 使用步骤:
#   1. 终端里启动下载服务:
#        xestudy-downlaod --savepath ./videos
#      不确定服务是否活着时, 浏览器访问 http://127.0.0.1:8010/healthz
#   2. Reqable 菜单 Traffic -> Script -> Enable 打开脚本功能;
#   3. Traffic -> Script -> Add Rule 新建规则:
#        规则名:   小鹅通视频下载
#        匹配 URL: https://app.xiaoe-tech.com/query_video_info
#   4. 把本文件内容整体粘贴到规则脚本编辑区, 保存后即可;
#   5. 在小鹅通客户端/小程序里播放视频, 服务端会自动收到下载任务.
#
# 说明:
#   - 脚本只依赖 Python 标准库, 无需 pip 安装额外的包;
#   - onRequest 与 onResponse 运行在不同进程, 本脚本只使用 onResponse;
#   - 服务地址默认是本机 8010 端口, 需要改端口时优先用 Reqable 环境变量 XESTUDY_SERVER,
#     其次才改下面的 SERVER_URL.

from reqable import *

import json
import traceback
import urllib.request

# 下载服务的提交接口, 端口需与 --bind 一致.
SERVER_URL = 'http://127.0.0.1:8010/upload-video-info'

# 只处理这个路径的响应.
TARGET_PATH = '/query_video_info'

# 提交超时时间, 单位秒.
TIMEOUT = 10.0


def _server_url(context):
    """优先读取 Reqable 环境变量, 方便在不改脚本的情况下切换服务地址."""
    try:
        address = context.env.get('XESTUDY_SERVER')
    except Exception:
        address = None
    return address or SERVER_URL


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


def onResponse(context, response):
    # 规则本身已经按 URL 匹配, 这里再确认一次路径, 避免规则被改宽后误动作.
    if not context.url.split('?', 1)[0].endswith(TARGET_PATH):
        return response

    try:
        payload = json.loads(str(response.body))
    except Exception:
        print('[xestudy] 响应体不是 JSON, 已忽略')
        traceback.print_exc()
        return response

    if payload.get('code') != 0:
        print('[xestudy] 接口返回 code=%s, 跳过: %s' % (payload.get('code'), payload.get('msg')))
        return response

    data = payload.get('data') or {}
    content = data.get('0') or {}
    video_info = data.get('video_info') or {}
    if not video_info.get('src'):
        print('[xestudy] 响应里没有 data.video_info.src, 跳过')
        return response

    print('[xestudy] 捕获视频: %s' % (content.get('file_name') or content.get('title') or '未知'))
    print('[xestudy] m3u8 地址: %s' % video_info.get('src'))

    try:
        status, text = _submit(_server_url(context), payload)
        print('[xestudy] 已提交下载任务: %s %s' % (status, text.strip()))
    except Exception as error:
        print('[xestudy] 提交下载任务失败: %s' % error)
        print('[xestudy] 请确认下载服务已启动, 且地址与 SERVER_URL 一致')
        traceback.print_exc()

    # 不改动原始响应, 保证抓包内容与真实响应一致.
    return response
