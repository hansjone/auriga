package observer

import (
	"os"
	"path/filepath"
	"time"
)

func scheduleFilesChanged(rootDir, instanceID string, lastMod map[string]time.Time) bool {
	paths := []string{
		filepath.Join(rootDir, "ops-cron", "store.json"),
		filepath.Join(rootDir, "storages", "schedule.json"),
	}
	changed := false
	for _, p := range paths {
		key := instanceID + "|" + p
		st, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				if _, ok := lastMod[key]; ok {
					delete(lastMod, key)
					changed = true
				}
				continue
			}
			continue
		}
		mod := st.ModTime()
		prev, ok := lastMod[key]
		if !ok || !mod.Equal(prev) {
			lastMod[key] = mod
			changed = true
		}
	}
	return changed
}
