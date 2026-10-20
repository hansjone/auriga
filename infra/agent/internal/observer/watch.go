package observer

import (
	"time"
)

func scheduleFilesChanged(rootDir, instanceID string, lastMod map[string]time.Time) bool {
	paths := []string{
		"ops-cron/store.json",
		"storages/schedule.json",
	}
	changed := false
	for _, p := range paths {
		key := instanceID + "|" + p
		f, err := openSchedule(rootDir, p)
		if err != nil {
			if _, ok := lastMod[key]; ok {
				delete(lastMod, key)
				changed = true
			}
			continue
		}
		st, err := f.Stat()
		_ = f.Close()
		if err != nil {
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
