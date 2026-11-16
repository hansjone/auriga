package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
)

var volumeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type volumeRecord struct {
	Name    string            `json:"name"`
	Driver  string            `json:"driver"`
	Labels  map[string]string `json:"labels"`
	Options map[string]string `json:"options"`
	File    string            `json:"file"`
	SHA256  string            `json:"sha256"`
}

func configuredVolumes(s state) ([]volumeRecord, error) {
	var out bytes.Buffer
	if err := compose(s, &out, "config", "--format", "json"); err != nil {
		return nil, err
	}
	var config struct {
		Volumes map[string]struct {
			Name string `json:"name"`
		} `json:"volumes"`
	}
	if err := json.Unmarshal(out.Bytes(), &config); err != nil {
		return nil, err
	}
	var result []volumeRecord
	seenNames := map[string]bool{}
	keys := make([]string, 0, len(config.Volumes))
	for key := range config.Volumes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		name := config.Volumes[key].Name
		if !volumeName.MatchString(name) {
			return nil, fmt.Errorf("卷名无效 %s", name)
		}
		if seenNames[name] {
			continue
		}
		seenNames[name] = true
		raw, err := capture("docker", "volume", "ls", "--format", "{{.Name}}", "--filter", "name=^"+regexp.QuoteMeta(name)+"$")
		if err != nil {
			return nil, err
		}
		if raw == "" {
			continue
		} // An unused optional profile may never have created its volume.
		record, err := inspectVolume(name)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func inspectVolume(name string) (volumeRecord, error) {
	var records []volumeRecord
	raw, err := capture("docker", "volume", "inspect", name)
	if err != nil {
		return volumeRecord{}, err
	}
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return volumeRecord{}, err
	}
	if len(records) != 1 || records[0].Name != name || records[0].Driver != "local" {
		return volumeRecord{}, fmt.Errorf("卷 %s 需要 local 驱动及明确身份，未执行备份或恢复", name)
	}
	return records[0], nil
}

func volumesIdle(records []volumeRecord) error {
	for _, v := range records {
		ids, err := capture("docker", "ps", "-q", "--filter", "volume="+v.Name)
		if err != nil {
			return err
		}
		if ids != "" {
			return fmt.Errorf("卷 %s 仍被运行容器使用，请先停止消费者", v.Name)
		}
	}
	return nil
}

func volumeHelper(s state, name string, readonly bool) []string {
	mount := "type=volume,src=" + name + ",dst=/volume,volume-nocopy"
	if readonly {
		mount += ",readonly"
	}
	return []string{"run", "--interactive", "--rm", "--pull", "never", "--network", "none", "--log-driver", "none", "--user", "0:0", "--read-only", "--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--security-opt", "no-new-privileges:true", "--mount", mount, "--entrypoint", "sh", s.Images["POSTGRES"], "-ec"}
}

func snapshotVolumes(s state, records []volumeRecord, root string) error {
	if err := volumesIdle(records); err != nil {
		return err
	}
	dir := filepath.Join(root, "volumes")
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	for i := range records {
		records[i].File = fmt.Sprintf("%d.tar", i)
		file := filepath.Join(dir, records[i].File)
		out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		args := append(volumeHelper(s, records[i].Name, true), "tar --numeric-owner -cpf - -C /volume .")
		err = command(nil, out, nil, "docker", args...)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		records[i].SHA256, err = digest(file)
		if err != nil {
			return err
		}
	}
	if records == nil {
		records = []volumeRecord{}
	}
	return jsonFile(filepath.Join(dir, "manifest.json"), records, 0600)
}

func restoreVolumes(s state, root string) error {
	dir := filepath.Join(root, "volumes")
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		fmt.Println("旧备份未包含 named volumes，请另核卷数据。")
		return nil
	}
	var records []volumeRecord
	if err := readJSON(filepath.Join(dir, "manifest.json"), &records); err != nil {
		return err
	}
	seen := map[string]bool{}
	// Validate every archive and current volume before altering any data.
	for i, v := range records {
		if !volumeName.MatchString(v.Name) || seen[v.Name] || v.File != fmt.Sprintf("%d.tar", i) || !hex64.MatchString(v.SHA256) {
			return errors.New("卷备份清单无效")
		}
		seen[v.Name] = true
		if err := noSymlinks(filepath.Join(dir, v.File)); err != nil {
			return err
		}
		hash, err := digest(filepath.Join(dir, v.File))
		if err != nil {
			return err
		}
		if hash != v.SHA256 {
			return fmt.Errorf("卷归档摘要不符 %s", v.Name)
		}
		actual, err := inspectVolume(v.Name)
		if err != nil {
			return err
		}
		if actual.Driver != v.Driver || !reflect.DeepEqual(actual.Options, v.Options) || !reflect.DeepEqual(actual.Labels, v.Labels) {
			return fmt.Errorf("卷身份已改变 %s", v.Name)
		}
	}
	if err := volumesIdle(records); err != nil {
		return err
	}
	for _, v := range records {
		in, err := os.Open(filepath.Join(dir, v.File))
		if err != nil {
			return err
		}
		// Only this explicitly inspected volume is writable. A retry repeats the
		// complete restore while all consumers remain stopped.
		args := append(volumeHelper(s, v.Name, false), "find /volume -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +; tar --same-owner -xpf - -C /volume")
		err = command(in, io.Discard, nil, "docker", args...)
		closeErr := in.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
