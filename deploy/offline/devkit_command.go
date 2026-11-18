package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func devkit(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "verify":
			return devkitVerify(args[1:])
		case "merge":
			return devkitMergeCommand(args[1:])
		}
	}
	f := flags("devkit")
	index := f.String("release", "auriga-release.json", "")
	out := f.String("out", "", "")
	previous := f.String("previous-devkit", "", "")
	delta := f.String("delta-out", "", "")
	key := f.String("sign-key", "", "")
	reset := f.Bool("new-baseline", false, "")
	max := f.Int64("max-devkit-bytes", 64<<30, "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *out == "" || *key == "" || (*previous == "" && !*reset) {
		return errors.New("devkit 需要 --out、--sign-key，以及 --previous-devkit 上版完整套件目录或 --new-baseline")
	}
	source, err := os.Getwd()
	if err != nil {
		return err
	}
	var r release
	if err := readJSON(*index, &r); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "auriga-cumulative-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	singleTar := filepath.Join(stage, "single.tar")
	if err := devkitSingle([]string{"--release", *index, "--out", singleTar, "--sign-key", *key}); err != nil {
		return err
	}
	single := filepath.Join(stage, "single")
	if err := extractVerified(singleTar, single, ""); err != nil {
		return err
	}
	commit := ""
	if _, err := os.Stat(filepath.Join(source, "auriga-source.json")); err == nil {
		m, err := readSourceManifest(source)
		if err != nil {
			return err
		}
		commit = m.Source.Commit
	} else {
		commit, err = gitAt(source, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
	}
	full := filepath.Join(stage, "full")
	cat, err := composeDevkit(*previous, single, r.Version, commit, full, *key, *max, *reset)
	if err != nil {
		return err
	}
	if *previous != "" && cat.Baseline != r.Version {
		if *delta == "" {
			*delta = strings.TrimSuffix(*out, ".tar") + "-delta.tar"
		}
		if err := writeDevkitDelta(*previous, full, *delta, *key); err != nil {
			return err
		}
		fmt.Printf("增量 %s\n", *delta)
	}
	if err := pack(full, *out); err != nil {
		return err
	}
	fmt.Printf("累积套件 %s；基线 %s，最新 %s，覆盖%d版\n", *out, cat.Baseline, cat.Latest, len(cat.Versions))
	return nil
}
