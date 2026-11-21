package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

// imageResolveLimit 是调用引导程序 image-extra-resolve（检查 + 重导出一个镜像）的外层上限：
// 引导程序自己的检查上限 + 导出总上限 + 余量，保证引导程序先按自己的时限给出中文报错。
func imageResolveLimit(lookup func(string) (string, bool)) time.Duration {
	limit := 10*time.Minute + 2*time.Hour
	if v, ok := lookup("AURIGA_DOCKER_INSPECT_TIMEOUT"); ok && v != "" {
		// 非法或超范围的值按默认算，由引导程序自己报“环境变量非法”。
		if d, err := time.ParseDuration(v); err == nil && d >= time.Minute && d <= 24*time.Hour {
			limit += d - 10*time.Minute
		}
	}
	if v, ok := lookup("AURIGA_DOCKER_IMAGE_SAVE_MAX"); ok && v != "" {
		// 非法或超范围的值按默认算，由引导程序自己报“环境变量非法”。
		if d, err := time.ParseDuration(v); err == nil && d >= time.Minute && d <= 24*time.Hour {
			limit += d - 2*time.Hour
		}
	}
	return limit + 10*time.Minute
}

// captureImageResolve 用 imageResolveLimit 作外层上限调用引导程序重导出镜像。
func captureImageResolve(ctx context.Context, boot string, args ...string) (string, error) {
	var b bytes.Buffer
	limit := imageResolveLimit(os.LookupEnv)
	err := commandContextLimit(ctx, limit, nil, &b, nil, boot, append([]string{"image-extra-resolve"}, args...)...)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("重导出镜像超过外层上限 %s；现场负载高或磁盘慢时可调大 AURIGA_DOCKER_IMAGE_SAVE_MAX: %w", limit, err)
	}
	return strings.TrimSpace(b.String()), err
}
