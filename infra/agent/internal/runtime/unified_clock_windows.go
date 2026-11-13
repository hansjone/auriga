package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"golang.org/x/sys/windows"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var unifiedWindowsBoot struct {
	sync.Once
	id string
}
var unifiedTickCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

func unifiedHostClock() (string, func() int64) {
	unifiedWindowsBoot.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		raw, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')").Output()
		if err != nil {
			return
		}
		stamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
		if err != nil {
			return
		}
		digest := sha256.Sum256([]byte(stamp.UTC().Format(time.RFC3339Nano)))
		digest[6] = (digest[6] & 15) | 64
		digest[8] = (digest[8] & 63) | 128
		value := hex.EncodeToString(digest[:16])
		unifiedWindowsBoot.id = value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:]
	})
	return unifiedWindowsBoot.id, func() int64 {
		if unifiedTickCount.Find() != nil {
			return -1
		}
		value, _, _ := unifiedTickCount.Call()
		return int64(value)
	}
}
