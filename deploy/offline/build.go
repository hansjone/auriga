package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The recipe archive is the reviewed FORGEJO-SELF-HOSTED-DEPS source snapshot.
// Its scripts enforce language locks and run the actual offline builds. This
// wrapper only downloads approved inputs and orchestrates those existing steps.
func buildRelease(root, stage string, r release) error {
	if r.Build == nil {
		return errors.New("--build 需要发行索引 build 字段，复用 Forgejo 依赖分支配方")
	}
	b := r.Build
	inputs := filepath.Join(stage, "inputs")
	recipes := filepath.Join(stage, "recipes")
	source := filepath.Join(stage, "source")
	for name, a := range map[string]artifact{"inputs": b.Inputs, "recipes": b.Recipes} {
		archive := filepath.Join(stage, name+".tar")
		if err := fetch(a, archive); err != nil {
			return err
		}
		dir := filepath.Join(stage, name)
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		if err := unpack(archive, dir); err != nil {
			return err
		}
	}
	// git archive fixes source identity and avoids shipping untracked secrets.
	archive := filepath.Join(stage, "source.tar")
	if err := command(nil, os.Stdout, nil, "git", "archive", "--format=tar", "--output", archive, "HEAD"); err != nil {
		return err
	}
	if err := os.Mkdir(source, 0755); err != nil {
		return err
	}
	if err := unpack(archive, source); err != nil {
		return err
	}
	recipeTarget := filepath.Join(source, "deploy/nas/offline-build")
	if err := os.RemoveAll(recipeTarget); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(recipes, "deploy/nas/offline-build"), recipeTarget); err != nil {
		return err
	}
	output := filepath.Join(stage, "output")
	if err := os.Mkdir(output, 0755); err != nil {
		return err
	}
	for _, kind := range []string{"go", "web", "java"} {
		tool := b.Tools[kind]
		if !immutable.MatchString(tool) {
			return errors.New("构建工具镜像必须固定 digest")
		}
		if err := command(nil, os.Stdout, nil, "docker", "pull", "--platform", "linux/amd64", tool); err != nil {
			return err
		}
		if err := os.Mkdir(filepath.Join(output, kind), 0755); err != nil {
			return err
		}
		envName := "AURIGA_" + map[string]string{"go": "GO_PROXY_DIR", "web": "WEB_INPUTS_DIR", "java": "JAVA_INPUTS_DIR"}[kind] + "=/inputs"
		args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--platform", "linux/amd64", "--mount", "type=bind,src=" + source + ",dst=/source,readonly", "--mount", "type=bind,src=" + filepath.Join(inputs, kind) + ",dst=/inputs,readonly", "--mount", "type=bind,src=" + filepath.Join(output, kind) + ",dst=/output", "--env", envName, tool, "sh", "/source/deploy/nas/offline-build/" + kind + "-inputs.sh", "build", "/output"}
		if err := command(nil, os.Stdout, nil, "docker", args...); err != nil {
			return err
		}
	}
	if err := copyTree(filepath.Join(inputs, "system"), filepath.Join(output, "system")); err != nil {
		return err
	}
	for _, service := range []string{"platform", "edge", "agent", "bifrost", "operator"} {
		base := b.Bases[service]
		if !immutable.MatchString(base) {
			return fmt.Errorf("%s 装配基镜像必须固定 digest", service)
		}
		if err := command(nil, os.Stdout, nil, "docker", "pull", "--platform", "linux/amd64", base); err != nil {
			return err
		}
		if err := command(nil, os.Stdout, nil, "sh", filepath.Join(source, "deploy/nas/offline-build/assemble-image.sh"), service, "auriga/"+service+":nas", base, output); err != nil {
			return err
		}
		if err := command(nil, os.Stdout, nil, "docker", "save", "--platform", "linux/amd64", "-o", filepath.Join(root, "images", service+".tar"), "auriga/"+service+":nas"); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(root, "deploy-nas")); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(source, "deploy/nas"), filepath.Join(root, "deploy-nas")); err != nil {
		return err
	}
	for _, name := range []string{"auriga-nas-bootstrap", "runtime-release"} {
		target := filepath.Join(root, "deploy-nas/bin", name)
		if err := copyFile(filepath.Join(output, "go", name), target, 0755); err != nil {
			return err
		}
	}
	boot := filepath.Join(root, "deploy-nas/bin/auriga-nas-bootstrap")
	metadata, err := capture(boot, "image-metadata", filepath.Join(root, "images"))
	if err != nil {
		return err
	}
	var proofs map[string]map[string]any
	if err := json.Unmarshal([]byte(metadata), &proofs); err != nil {
		return err
	}
	var manifest map[string]any
	if err := readJSONLoose(filepath.Join(root, "images/manifest.json"), &manifest); err != nil {
		return err
	}
	for name, proof := range proofs {
		id, err := capture("docker", "image", "inspect", "--format", "{{.Id}}", "auriga/"+name+":nas")
		if err != nil {
			return err
		}
		proof["id"], proof["tar"] = id, name+".tar"
	}
	manifest["images"] = proofs
	commit, err := capture("git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	manifest["commit"] = commit
	tree, err := capture("git", "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	manifest["source_tree"] = tree
	for key, name := range map[string]string{"bootstrap": "auriga-nas-bootstrap", "runtime_release": "runtime-release"} {
		file := "deploy-nas/bin/" + name
		hash, err := digest(filepath.Join(root, file))
		if err != nil {
			return err
		}
		manifest[key] = map[string]string{"file": file, "sha256": hash, "os": "linux", "arch": "amd64"}
		if key == "bootstrap" {
			manifest[key] = map[string]string{"file": file, "sha256": hash}
		}
	}
	if err := jsonFile(filepath.Join(root, "images/manifest.json"), manifest, 0644); err != nil {
		return err
	}
	if err := writeImageSums(root); err != nil {
		return err
	}
	return command(nil, os.Stdout, nil, boot, "image-check", root)
}

func writeImageSums(root string) error {
	var sums string
	for _, name := range []string{"platform", "edge", "agent", "bifrost", "operator"} {
		hash, err := digest(filepath.Join(root, "images", name+".tar"))
		if err != nil {
			return err
		}
		sums += hash + "  ./" + name + ".tar\n"
	}
	if err := os.WriteFile(filepath.Join(root, "images/SHA256SUMS"), []byte(sums), 0644); err != nil {
		return err
	}
	sums = ""
	for _, file := range []string{"deploy-nas/bin/auriga-nas-bootstrap", "deploy-nas/bin/runtime-release", "images/manifest.json", "versions.txt"} {
		hash, err := digest(filepath.Join(root, file))
		if err != nil {
			return err
		}
		sums += hash + "  " + file + "\n"
	}
	for _, file := range []string{"SHA256SUMS", "NAS-SHA256SUMS"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(sums), 0644); err != nil {
			return err
		}
	}
	return nil
}
