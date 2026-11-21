package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The recipe archive is the reviewed FORGEJO-SELF-HOSTED-DEPS source snapshot.
// Its scripts enforce language locks and run the actual offline builds. This
// wrapper only downloads approved inputs and orchestrates those existing steps.
func buildRelease(root, stage string, r release, options buildOptions) error {
	ctx := options.context()
	if r.Build == nil {
		return errors.New("--build 需要发行索引 build 字段，复用 Forgejo 依赖分支配方")
	}
	b := r.Build
	inputs := filepath.Join(stage, "inputs")
	recipes := filepath.Join(stage, "recipes")
	source := filepath.Join(stage, "source")
	for name, a := range map[string]artifact{"inputs": b.Inputs, "recipes": b.Recipes} {
		archive := filepath.Join(stage, name+".tar")
		if err := fetchInput(a, archive, options); err != nil {
			return err
		}
		dir := filepath.Join(stage, name)
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		if err := unpackContext(ctx, archive, dir); err != nil {
			return err
		}
	}
	if options.Online {
		report, err := collectOnlineInputs(ctx, stage, r, options)
		if err != nil {
			return err
		}
		if report != nil {
			report.LockDigest, report.ChangedLockFiles = options.LockDigest, options.ChangedLockFiles
			if err := jsonFile(filepath.Join(root, "site-dependencies.json"), report, 0644); err != nil {
				return err
			}
		}
	}
	recipeTarget := filepath.Join(source, "deploy/nas/offline-build")
	if err := os.RemoveAll(recipeTarget); err != nil {
		return err
	}
	if err := copyTreeContext(ctx, filepath.Join(recipes, "deploy/nas/offline-build"), recipeTarget); err != nil {
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
		if err := prepareBuildImageContext(ctx, tool, options.Offline); err != nil {
			return err
		}
		if err := os.Mkdir(filepath.Join(output, kind), 0755); err != nil {
			return err
		}
		envName := "AURIGA_" + map[string]string{"go": "GO_PROXY_DIR", "web": "WEB_INPUTS_DIR", "java": "JAVA_INPUTS_DIR"}[kind] + "=/inputs"
		args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--platform", "linux/amd64", "--mount", "type=bind,src=" + source + ",dst=/source,readonly", "--mount", "type=bind,src=" + filepath.Join(inputs, kind) + ",dst=/inputs,readonly", "--mount", "type=bind,src=" + filepath.Join(output, kind) + ",dst=/output", "--env", envName, tool, "sh", "/source/deploy/nas/offline-build/" + kind + "-inputs.sh", "build", "/output"}
		args = append(append([]string{"run"}, options.Budget.dockerArgs()...), args[1:]...)
		if err := commandContext(ctx, nil, os.Stdout, nil, "docker", args...); err != nil {
			return err
		}
	}
	if err := copyTreeContext(ctx, filepath.Join(inputs, "system"), filepath.Join(output, "system")); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(source, "deploy/offline/go.mod")); err == nil {
		args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--platform", "linux/amd64", "--mount", "type=bind,src=" + source + ",dst=/source,readonly", "--mount", "type=bind,src=" + filepath.Join(inputs, "go") + ",dst=/inputs,readonly", "--mount", "type=bind,src=" + filepath.Join(output, "go") + ",dst=/output", "--env", "GOPROXY=file:///inputs/download", "--env", "GOSUMDB=off", "--env", "GOTOOLCHAIN=local", "--env", "CGO_ENABLED=0", "--workdir", "/source/deploy/offline", b.Tools["go"], "go", "build", "-mod=readonly", "-o", "/output/auriga-cli", "."}
		args = append(append([]string{"run"}, options.Budget.dockerArgs()...), args[1:]...)
		if err := commandContext(ctx, nil, os.Stdout, nil, "docker", args...); err != nil {
			return err
		}
	}
	if err := jsonFile(filepath.Join(output, "web/admin/provenance.json"), options.Info, 0644); err != nil {
		return err
	}
	for _, service := range []string{"platform", "edge", "agent", "bifrost", "operator"} {
		base := b.Bases[service]
		if !options.Offline && !immutable.MatchString(base) {
			return fmt.Errorf("%s 装配基镜像必须固定 digest", service)
		}
		if err := prepareBuildImageContext(ctx, base, options.Offline); err != nil {
			return err
		}
		// BuildKit treats a bare sha256 image ID in FROM as a registry name.
		// Give an already verified local ID a digest-derived tag before handing
		// it to the shared assembly recipe, just as devkit loading does.
		if strings.HasPrefix(base, "sha256:") {
			tag := kitImageTag("base-"+service, base)
			if err := commandContext(ctx, nil, os.Stdout, nil, "docker", "tag", base, tag); err != nil {
				return err
			}
			base = tag
		}
		// Buildx otherwise probes Git even for a source archive without .git.
		// Provenance is supplied by our signed source manifest instead.
		if err := commandContext(ctx, nil, os.Stdout, []string{"BUILDX_GIT_INFO=false", "BUILDX_GIT_LABELS=false"}, "sh", filepath.Join(source, "deploy/nas/offline-build/assemble-image.sh"), service, "auriga/"+service+":nas", base, output); err != nil {
			return err
		}
		if err := commandContext(ctx, nil, os.Stdout, nil, "docker", "save", "--platform", "linux/amd64", "-o", filepath.Join(root, "images", service+".tar"), "auriga/"+service+":nas"); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(root, "deploy-nas")); err != nil {
		return err
	}
	if err := copyTreeContext(ctx, filepath.Join(source, "deploy/nas"), filepath.Join(root, "deploy-nas")); err != nil {
		return err
	}
	for _, name := range []string{"auriga-nas-bootstrap", "runtime-release"} {
		target := filepath.Join(root, "deploy-nas/bin", name)
		if err := copyFileContext(ctx, filepath.Join(output, "go", name), target, 0755); err != nil {
			return err
		}
	}
	boot := filepath.Join(root, "deploy-nas/bin/auriga-nas-bootstrap")
	metadata, err := captureContext(ctx, boot, "image-metadata", filepath.Join(root, "images"))
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
		id, err := captureContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", "auriga/"+name+":nas")
		if err != nil {
			return err
		}
		proof["id"], proof["tar"] = id, name+".tar"
	}
	manifest["images"] = proofs
	manifest["commit"] = options.Info.Commit
	tree, err := sourceTreeContext(ctx, options.Source)
	if err != nil {
		return err
	}
	manifest["source_tree"] = tree
	for key, name := range map[string]string{"bootstrap": "auriga-nas-bootstrap", "runtime_release": "runtime-release"} {
		file := "deploy-nas/bin/" + name
		hash, err := digestContext(ctx, filepath.Join(root, file))
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
	if err := writeImageSumsContext(ctx, root); err != nil {
		return err
	}
	return commandContext(ctx, nil, os.Stdout, nil, boot, "image-check", root)
}

func prepareBuildImageContext(ctx context.Context, ref string, offline bool) error {
	if !offline && strings.Contains(ref, "@sha256:") {
		return commandContext(ctx, nil, os.Stdout, nil, "docker", "pull", "--platform", "linux/amd64", ref)
	}
	platform, err := captureContext(ctx, "docker", "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", ref)
	if err != nil || platform != "linux/amd64" {
		return fmt.Errorf("缺少本地 linux/amd64 构建镜像 %s，请重新准备 devkit", ref)
	}
	return nil
}

func writeImageSumsContext(ctx context.Context, root string) error {
	var sums string
	for _, name := range []string{"platform", "edge", "agent", "bifrost", "operator"} {
		hash, err := digestContext(ctx, filepath.Join(root, "images", name+".tar"))
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
		hash, err := digestContext(ctx, filepath.Join(root, file))
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
