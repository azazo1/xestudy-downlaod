# 小鹅通抓包诊断 - Reqable 脚本 (Addon)
#
# 作用:
#   把所有含指定片段的请求与响应都回传给本机的 xestudy-downlaod 服务 (需带 --sniff 启动),
#   由服务端分析视频信息究竟来自哪个接口, 用于排查 "query_video_info 返回未找到" 这类问题.
#   本脚本只采集, 不下载; 下载由 query-video-info.py 那个规则负责.
#
# 使用步骤:
#   1. 带诊断模式启动下载服务:
#        xestudy-downlaod --sniff --savepath ./videos
#   2. Reqable 菜单 Traffic -> Script -> Enable 打开脚本功能;
#   3. Traffic -> Script -> Add Rule 新建规则 (与下载规则并存, 互不影响):
#        规则名:   小鹅通抓包诊断
#        匹配 URL: *xiaoe*
#   4. 把本文件内容整体粘贴到规则脚本编辑区, 保存;
#   5. 在小鹅通里点开视频并播放几秒, 然后看服务端日志, 或访问:
#        http://127.0.0.1:8010/sniff/report
#      报告里会指出哪个接口的响应带 m3u8, 以及 reqable 规则匹配地址该怎么写.
#   6. 重新开始一轮诊断前可以清空记录:
#        curl -X DELETE http://127.0.0.1:8010/sniff
#
# 说明:
#   - onRequest 与 onResponse 两个入口都必须定义, 运行器会分别无条件调用它们;
#   - 脚本只依赖 Python 标准库, 无需 pip 安装额外的包;
#   - 正文超过 MAX_BODY 会被截断, 避免大响应拖慢抓包;
#   - 需要改采集范围或服务地址时, 优先用 Reqable 环境变量:
#       XESTUDY_SNIFF_MATCHES   逗号分隔的片段, 例如 "xiaoe,xet.tech"
#       XESTUDY_SNIFF_SERVER    例如 http://127.0.0.1:8011/sniff

from reqable import *

import json
import traceback
import urllib.request

# 诊断服务地址, 与 --bind 一致.
SERVER_URL = 'http://127.0.0.1:8010/sniff'

# URL 里含其中任一片段就会被采集.
MATCHES = ['xiaoe']

# 单条正文的最大长度, 超出部分截断.
MAX_BODY = 256 * 1024

# 回传超时时间, 单位秒; 诊断服务没开时也只会短暂等待.
TIMEOUT = 5.0


def _text_from_env(context, name, default):
    try:
        value = context.env.get(name)
    except Exception:
        value = None
    return value or default


def _should_collect(context):
    """判断该请求是否在采集范围内."""
    raw = _text_from_env(context, 'XESTUDY_SNIFF_MATCHES', '')
    patterns = [item.strip() for item in raw.split(',') if item.strip()] or MATCHES
    url = context.url or ''
    for pattern in patterns:
        if pattern and pattern in url:
            return True
    return False


def _body_text(body):
    """把 HttpBody 转成文本并截断, 返回 (文本, 是否截断)."""
    if body is None or getattr(body, 'isNone', False):
        return '', False

    try:
        text = str(body)
    except Exception:
        return '', False

    if len(text) > MAX_BODY:
        return text[:MAX_BODY], True
    return text, False


def _submit(context, payload):
    address = _text_from_env(context, 'XESTUDY_SNIFF_SERVER', SERVER_URL)
    body = json.dumps(payload, ensure_ascii=False).encode('utf-8')
    request = urllib.request.Request(
        address,
        data=body,
        headers={'Content-Type': 'application/json; charset=utf-8'},
    )
    with urllib.request.urlopen(request, timeout=TIMEOUT) as response:
        return response.status


def _uid(context):
    try:
        return str(context.uid)
    except Exception:
        return ''


def onRequest(context, request):
    # 运行器对每个请求都会无条件调用本函数, 因此即使不采集也必须定义并原样返回.
    if not _should_collect(context):
        return request

    try:
        text, truncated = _body_text(request.body)
        _submit(context, {
            'uid': _uid(context),
            'phase': 'request',
            'url': context.url,
            'method': request.method,
            'request_body': text,
            'truncated': truncated,
        })
    except Exception as error:
        print('[xestudy-sniff] 回传请求失败: %s' % error)

    return request


def onResponse(context, response):
    if not _should_collect(context):
        return response

    try:
        text, truncated = _body_text(response.body)
        method = ''
        try:
            method = response.request.method
        except Exception:
            method = ''

        _submit(context, {
            'uid': _uid(context),
            'phase': 'response',
            'url': context.url,
            'method': method,
            'status': response.code,
            'response_body': text,
            'truncated': truncated,
        })

        # 只有看起来相关的响应才打印, 免得控制台被普通请求淹没.
        if 'm3u8' in text or 'video_info' in text:
            print('[xestudy-sniff] 候选响应 %s (含 %s)' % (
                context.url,
                'm3u8' if 'm3u8' in text else 'video_info',
            ))
    except Exception as error:
        print('[xestudy-sniff] 回传响应失败: %s' % error)
        traceback.print_exc()

    return response
