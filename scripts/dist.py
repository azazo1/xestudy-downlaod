#!/usr/bin/env python3

# 生成发布产物 (Go), 复制到项目 scripts/dist.py.
# 需要同时复制 scripts/archive.py, scripts/build-version.py 与 scripts/release_common.py.
#
# 不带参数: 只构建当前平台, 并真的执行一次冒烟检查.
# --all: 在一个平台上交叉编译全部目标. 交叉产物的冒烟换成文件格式, 架构与版本串校验,
#        因此这条路径只适用于依赖链里没有 cgo 的项目.

import os
import sys

import release_common as rc

# 只改下面几个常量. Go 没有包版本可读, 仓库还没有版本 tag 时用占位版本兜底.
PROJECT_NAME = "xestudy-downlaod"
MAIN_PACKAGE = "./cmd/xestudy-downlaod"
BINARY_NAME = "xestudy-downlaod"
PACKAGE_VERSION = "0.0.0"
# 冒烟检查用的参数, 要求二进制能报出版本号.
SMOKE_ARGS = ["--version"]

# --all 时交叉编译的全部目标, 顺序固定, 便于日志与产物清单稳定.
DEFAULT_TARGETS = [
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
    ("windows", "amd64"),
    ("windows", "arm64"),
]

# go env GOOS / GOARCH 到产物命名 token 的映射.
PLATFORM_BY_GOOS = {"darwin": "macos", "linux": "linux", "windows": "windows"}
ARCH_BY_GOARCH = {"amd64": "x86_64", "arm64": "aarch64"}


def naming_tokens(goos, goarch):
    """把 GOOS / GOARCH 换成产物名里的平台与架构 token."""
    target_platform = PLATFORM_BY_GOOS.get(goos)
    target_arch = ARCH_BY_GOARCH.get(goarch)
    if not target_platform or not target_arch:
        rc.die(f"无法识别目标平台 {goos or '?'}-{goarch or '?'}, 请检查 GOOS / GOARCH")
    return target_platform, target_arch


def host_target():
    """取当前构建机的目标平台与架构."""
    goos = rc.try_run(["go", "env", "GOOS"]) or ""
    goarch = rc.try_run(["go", "env", "GOARCH"]) or ""
    return goos, goarch


def ensure_cross_compilable():
    """交叉编译前确认依赖链里没有 cgo.

    有 cgo 时产物会绑定构建机的系统库, 交叉编译出来的东西在目标平台可能根本跑不起来,
    而且这种失效是静默的, 所以宁可直接拒绝, 让调用方回到原生矩阵构建.
    """
    listed = rc.run(
        [
            "go",
            "list",
            "-deps",
            "-f",
            "{{if .CgoFiles}}{{.ImportPath}}{{end}}",
            MAIN_PACKAGE,
        ]
    )
    cgo_packages = [line.strip() for line in listed.splitlines() if line.strip()]
    if cgo_packages:
        shown = ", ".join(cgo_packages[:5])
        more = "" if len(cgo_packages) <= 5 else f" 等 {len(cgo_packages)} 个包"
        rc.die(
            f"依赖链里有 cgo: {shown}{more}; 交叉编译产物可能无法在目标平台运行, "
            "请改用原生 runner 矩阵构建"
        )


def binary_name_for(goos):
    return BINARY_NAME + (".exe" if goos == "windows" else "")


def build(goos, goarch, display_version, binary_path):
    """按指定目标构建二进制, 注入版本号.

    注入路径必须是完整包路径, 所以这里用 go list -m 取当前模块名.
    """
    module = rc.run(["go", "list", "-m"])
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = goos
    env["GOARCH"] = goarch
    rc.run(
        [
            "go",
            "build",
            "-trimpath",
            "-ldflags",
            f"-s -w -X {module}/internal/buildinfo.version={display_version}",
            "-o",
            binary_path,
            MAIN_PACKAGE,
        ],
        env=env,
        capture=False,
    )


def main():
    rc.configure_output_encoding()
    rc.repo_root()

    all_platforms = "--all" in sys.argv[1:]
    unknown = [item for item in sys.argv[1:] if item != "--all"]
    if unknown:
        rc.die(f"未知参数: {' '.join(unknown)}, 只支持 --all")

    display_version, archive_version = rc.resolve_versions(
        rc.script_path("build-version.py"), PACKAGE_VERSION
    )
    host_goos, host_goarch = host_target()

    if all_platforms:
        ensure_cross_compilable()
        print(f"构建 {PROJECT_NAME} {display_version}, 交叉编译 {len(DEFAULT_TARGETS)} 个目标")
        for goos, goarch in DEFAULT_TARGETS:
            build_one(goos, goarch, display_version, archive_version, host_goos, host_goarch)
        print(f"全部平台产物已生成: {len(DEFAULT_TARGETS)} 个")
        return

    if (host_goos, host_goarch) not in DEFAULT_TARGETS:
        rc.die(f"当前目标 {host_goos}-{host_goarch} 不在产物命名表内, 请补充 GOOS / GOARCH 映射")
    build_one(host_goos, host_goarch, display_version, archive_version, host_goos, host_goarch)


def build_one(goos, goarch, display_version, archive_version, host_goos, host_goarch):
    """构建并打包一个目标; 只有宿主平台才真跑冒烟检查."""
    target_platform, target_arch = naming_tokens(goos, goarch)
    is_host = (goos, goarch) == (host_goos, host_goarch)
    print(f"构建 {PROJECT_NAME} {display_version} ({target_platform}-{target_arch})")

    binary = binary_name_for(goos)
    # 每个目标各用一个 staging 目录, 免得上一轮的同名二进制被一起打进归档.
    staging = rc.stage_dir(os.path.join("dist", "stage", f"{target_platform}-{target_arch}"))
    binary_path = os.path.join(staging, binary)

    build(goos, goarch, display_version, binary_path)

    if is_host:
        rc.smoke_check([binary_path], SMOKE_ARGS, display_version)
    else:
        # 交叉产物跑不起来, 改为核对它确实是目标平台与架构, 并且带着注入的版本号.
        rc.check_binary_format(binary_path, target_platform)
        rc.check_binary_arch(binary_path, target_platform, target_arch)
        rc.check_version_string(binary_path, display_version)

    rc.archive(
        staging,
        [binary],
        PROJECT_NAME,
        archive_version,
        target=(target_platform, target_arch),
    )


if __name__ == "__main__":
    main()
