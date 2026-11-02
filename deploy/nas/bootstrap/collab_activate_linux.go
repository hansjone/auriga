package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Recreate only the two already approved signer services, preserving their
// complete existing configuration. Never starts PG, platform, edge or agent.
// The public message-only start is the recovery entry point if interrupted.
func (e *nasRotationEngine) activate(r *nasRotation) error {
	if _, err := e.database(r); err != nil {
		return err
	}
	rows, err := r.releases()
	if err != nil {
		return err
	}
	dir, _, err := r.installed(rows, true)
	if err != nil {
		return err
	}
	all, err := e.containers()
	if err != nil {
		return err
	}
	platformRunning := false
	services := map[string]nasRotationContainer{}
	for _, c := range all {
		if c.Config.Labels["com.docker.compose.project"] != nasRotationProject {
			continue
		}
		s := c.Config.Labels["com.docker.compose.service"]
		if s == "platform" {
			if err = e.owned(r, c); err != nil {
				return err
			}
			platformRunning = c.State.Running && c.State.Health.Status == "healthy"
		}
		if s == "collab" || s == "network" {
			if _, ok := services[s]; ok {
				return fmt.Errorf("duplicate activation service")
			}
			if err = e.owned(r, c); err != nil {
				return err
			}
			services[s] = c
		}
	}
	if !platformRunning || len(services) != 2 {
		return fmt.Errorf("activation requires existing healthy platform and signer containers; recover with %s", r.messageRecoveryCommand())
	}
	runtimePath, err := r.gatewayRuntime(dir)
	if err != nil {
		return err
	}
	// A public Compose start may have used the legacy budget bind. Migrate only
	// through the same existing service/mount checks and signer-only stop gate.
	for _, c := range services {
		if c.Config.Labels["com.docker.compose.service"] != "network" || !c.State.Running {
			continue
		}
		matched := false
		for _, m := range c.Mounts {
			if m.Destination == "/etc/auriga/network/runtime.json" && m.Source == runtimePath && !m.RW {
				matched = true
			}
		}
		if !matched {
			if err = e.stop(r); err != nil {
				return err
			}
			for s, previous := range services {
				services[s], err = e.inspect(previous.ID)
				if err != nil {
					return err
				}
			}
		}
	}
	for _, s := range []string{"collab", "network"} {
		c := services[s]
		targets := map[string]string{}
		if s == "collab" {
			targets["/etc/auriga/collab"] = filepath.Join(dir, "config")
		} else {
			targets["/etc/auriga/network/protocol.key"] = filepath.Join(dir, "network/protocol.key")
			targets["/etc/auriga/network/runtime.json"] = runtimePath
		}
		ready := c.State.Running
		for target, source := range targets {
			found := false
			for _, m := range c.Mounts {
				if m.Destination == target && m.Source == source && !m.RW {
					found = true
				}
			}
			ready = ready && found
		}
		if ready {
			continue
		}
		if c.State.Running || c.State.Paused || c.State.Restarting {
			return fmt.Errorf("activation expects stopped signers; preserve checkpoint")
		}
		raw, err := e.call("GET", "/containers/"+c.ID+"/json", nil)
		if err != nil {
			return err
		}
		var original struct {
			Name            string
			Config          map[string]any
			HostConfig      map[string]any
			NetworkSettings struct {
				Networks map[string]struct{ Aliases []string }
			}
		}
		if json.Unmarshal(raw, &original) != nil || !strings.HasPrefix(original.Name, "/") {
			return fmt.Errorf("activation inspect refused")
		}
		// The exact previous definition is private crash evidence, never a new source
		// of approval and never sent to logs.
		backup := filepath.Join(r.root, "activation-"+s+"-"+c.ID+".json")
		if _, err = os.Lstat(backup); os.IsNotExist(err) {
			if err = nasRotationWrite(backup, raw); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		binds, ok := original.HostConfig["Binds"].([]any)
		if !ok {
			return fmt.Errorf("activation requires Compose bind definitions")
		}
		changed := map[string]bool{}
		for i, b := range binds {
			value, ok := b.(string)
			if !ok {
				return fmt.Errorf("activation bind refused")
			}
			parts := strings.Split(value, ":")
			if len(parts) != 3 {
				continue
			}
			if source, ok := targets[parts[1]]; ok {
				if !strings.Contains(parts[2], "ro") {
					return fmt.Errorf("activation writable signer config refused")
				}
				parts[0] = source
				binds[i] = strings.Join(parts, ":")
				changed[parts[1]] = true
			}
		}
		if len(changed) != len(targets) {
			return fmt.Errorf("activation bind target missing")
		}
		endpoints := map[string]any{}
		for name, n := range original.NetworkSettings.Networks {
			if name != nasRotationProject+"_internal" {
				return fmt.Errorf("activation foreign network refused")
			}
			aliases := []string{s}
			for _, a := range n.Aliases {
				if a == s || a == strings.TrimPrefix(original.Name, "/") {
					aliases = append(aliases, a)
				}
			}
			endpoints[name] = map[string]any{"Aliases": aliases}
		}
		if len(endpoints) != 1 {
			return fmt.Errorf("activation fixed internal network required")
		}
		original.HostConfig["Binds"] = binds
		body := original.Config
		body["Image"] = c.Image // Never resolve a previously used mutable tag again.
		body["HostConfig"] = original.HostConfig
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
		if _, err = e.call("DELETE", "/containers/"+c.ID, nil); err != nil {
			return err
		}
		created, err := e.call("POST", "/containers/create?name="+url.QueryEscape(strings.TrimPrefix(original.Name, "/")), body)
		var result struct {
			ID string `json:"Id"`
		}
		if err != nil || json.Unmarshal(created, &result) != nil || !nasRotationDigest.MatchString(result.ID) {
			return fmt.Errorf("signer recreation failed; run public start")
		}
		if _, err = e.call("POST", "/containers/"+result.ID+"/start", nil); err != nil {
			return err
		}
		if s == "collab" {
			deadline := time.Now().Add(150 * time.Second)
			for {
				current, err := e.inspect(result.ID)
				if err != nil || !current.State.Running {
					return fmt.Errorf("recreated collab unavailable")
				}
				if current.State.Health.Status == "healthy" {
					break
				}
				if !time.Now().Before(deadline) {
					return fmt.Errorf("recreated collab health deadline exceeded")
				}
				time.Sleep(time.Second)
			}
		}
	}
	return nil
}
