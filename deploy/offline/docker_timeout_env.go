package main

import (
	"fmt"
	"time"
)

// 构建会调用随套件的 auriga-nas-bootstrap 重导出镜像，它读取同名环境变量。
// 这里在开始构建前先校验，避免跑了很久才报“环境变量非法”。
var dockerImageTimeoutEnvs = []string{
	"AURIGA_DOCKER_IMAGE_STALL_TIMEOUT",
	"AURIGA_DOCKER_IMAGE_SAVE_MAX",
	"AURIGA_DOCKER_INSPECT_TIMEOUT",
}

func validateDockerImageTimeoutEnv(lookup func(string) (string, bool)) error {
	for _, name := range dockerImageTimeoutEnvs {
		raw, ok := lookup(name)
		if !ok || raw == "" {
			continue
		}
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("环境变量 %s 的值 %q 不是有效的时长；请写成 Go 时长，如 30m、2h", name, raw)
		}
		if d < time.Minute || d > 24*time.Hour {
			return fmt.Errorf("环境变量 %s 的值 %q 超出范围；须在 1m0s 到 24h0m0s 之间", name, raw)
		}
	}
	return nil
}
