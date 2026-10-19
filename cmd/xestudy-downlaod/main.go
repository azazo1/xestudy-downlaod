// xestudy-downlaod 是配合 reqable 使用的小鹅通视频下载服务.
package main

import (
	"os"

	"github.com/azazo1/xestudy-downlaod/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
